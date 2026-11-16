#!/usr/bin/env python3
"""Inventory the exact Cairn Linux/amd64 image saved and scanned by CI."""

import argparse
from datetime import UTC, datetime
import gzip
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile
import tempfile

OCI_INDEX = 'application/vnd.oci.image.index.v1+json'
DOCKER_INDEX = 'application/vnd.docker.distribution.manifest.list.v2+json'
IMAGE_MANIFESTS = {
    'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.v2+json',
}
IMAGE_PLATFORM = {'os': 'linux', 'architecture': 'amd64'}
MODULE = 'github.com/EduCloud-Ecosystem/cairn'


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def image_identity(path, revision):
    """Verify the saved index's runnable manifest, config and compressed layers."""
    with tarfile.open(path) as archive:
        members = {}
        for member in archive:
            if member.name in members:
                raise ValueError('duplicate path in image archive')
            members[member.name] = member

        def open_file(name):
            member = members.get(name)
            if not member or not member.isfile():
                raise ValueError('missing regular OCI archive member: ' + name)
            return archive.extractfile(member)

        def blob(descriptor, *, decode=False, diff_id=None):
            digest = descriptor['digest']
            if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
                raise ValueError('invalid OCI descriptor digest')
            with open_file('blobs/sha256/' + digest[7:]) as handle:
                if handle.seek(0, 2) != descriptor['size']:
                    raise ValueError('OCI descriptor size mismatch')
                handle.seek(0)
                if hashlib.file_digest(handle, 'sha256').hexdigest() != digest[7:]:
                    raise ValueError('OCI descriptor digest mismatch')
                if decode:
                    handle.seek(0)
                    return json.load(handle)
                if diff_id is not None:
                    handle.seek(0)
                    media_type = descriptor['mediaType']
                    if media_type.endswith(('+gzip', '.gzip')):
                        with gzip.GzipFile(fileobj=handle) as unpacked:
                            uncompressed = hashlib.file_digest(unpacked, 'sha256').hexdigest()
                    elif media_type == 'application/vnd.oci.image.layer.v1.tar':
                        uncompressed = hashlib.file_digest(handle, 'sha256').hexdigest()
                    else:
                        raise ValueError('unsupported saved-image layer encoding')
                    if 'sha256:' + uncompressed != diff_id:
                        raise ValueError('OCI layer does not match the config root filesystem')

        with open_file('index.json') as handle:
            index = json.load(handle)

        def manifests(index, depth=0):
            if depth > 4 or index['schemaVersion'] != 2:
                raise ValueError('unsupported OCI index')
            for entry in index['manifests']:
                platform = entry.get('platform', {})
                if platform and any(platform.get(k) != v for k, v in IMAGE_PLATFORM.items()):
                    continue
                media_type = entry['mediaType']
                if media_type in {OCI_INDEX, DOCKER_INDEX}:
                    yield from manifests(blob(entry, decode=True), depth + 1)
                elif media_type in IMAGE_MANIFESTS:
                    manifest = blob(entry, decode=True)
                    config = blob(manifest['config'], decode=True)
                    if all(config.get(k) == v for k, v in IMAGE_PLATFORM.items()):
                        yield entry, manifest, config

        selected = list(manifests(index))
        if len(selected) != 1:
            raise ValueError('expected exactly one runnable Linux/amd64 manifest')
        descriptor, manifest, config = selected[0]
        if config['config']['Labels']['org.opencontainers.image.revision'] != revision:
            raise ValueError('image source revision does not match the expected checkout')
        if (manifest['schemaVersion'] != 2 or not manifest['layers']
                or len(config['rootfs']['diff_ids']) != len(manifest['layers'])):
            raise ValueError('invalid image manifest layers')
        for layer, diff_id in zip(manifest['layers'], config['rootfs']['diff_ids'], strict=True):
            blob(layer, diff_id=diff_id)
        return descriptor['digest'], manifest['config']['digest']


def alpine_packages(path):
    packages = set()
    for record in path.read_text().strip().split('\n\n'):
        fields = dict(line.split(':', 1) for line in record.splitlines() if line[1:2] == ':')
        package = (fields.get('P'), fields.get('V'))
        if not all(package) or package in packages:
            raise ValueError('malformed or duplicate Alpine package record')
        packages.add(package)
    return packages


def go_packages(build_info):
    first, *lines = build_info.splitlines()
    _, toolchain = first.rsplit(': ', 1)
    if not re.fullmatch(r'go\d+\.\d+\.\d+', toolchain):
        raise ValueError('Go binary has no recognized toolchain version')
    packages = {('stdlib', 'v' + toolchain[2:])}
    path = ''
    settings = {}
    for line in lines:
        match line.lstrip('\t').split('\t'):
            case ['path', value]:
                path = value
            case ['build', value]:
                key, _, setting = value.partition('=')
                settings[key] = setting
            case ['dep', name, version, _] if name and version and version != '(devel)':
                packages.add((name, version))
            case ['dep', *_] | ['=>', *_]:
                raise ValueError('unversioned or replaced Go dependency needs separate provenance')
    if (path != MODULE + '/cmd/cairn' or settings.get('GOOS') != 'linux'
            or settings.get('GOARCH') != 'amd64' or len(packages) <= 1):
        raise ValueError('Go build information does not identify the Cairn Linux/amd64 binary')
    return packages


def dashboard_packages(evidence, web, lock_bytes):
    if evidence['schema'] != 1 or evidence['lock_sha256'] != sha256(lock_bytes):
        raise ValueError('dashboard build evidence does not match package-lock.json')
    files = {}
    for path in web.rglob('*'):
        if path.is_symlink():
            raise ValueError('dashboard includes a symbolic link')
        if path.is_file():
            files[path.relative_to(web).as_posix()] = sha256(path.read_bytes())
    if files != evidence['files'] or 'index.html' not in files:
        raise ValueError('dashboard inventory does not match the shipped file hashes')
    chunks = evidence['chunks']
    js_files = {name for name in files if PurePosixPath(name).suffix in {'.js', '.mjs', '.cjs'}}
    if not js_files or js_files != chunks.keys():
        raise ValueError('dashboard inventory does not cover every shipped JavaScript file')
    used = {path for packages in chunks.values() for path in packages}
    if not used or used != evidence['packages'].keys():
        raise ValueError('dashboard package records must be referenced by shipped chunks')
    lock = json.loads(lock_bytes)['packages']
    packages = set()
    for path, info in evidence['packages'].items():
        expected = lock.get(path)
        name = path.rsplit('node_modules/', 1)[-1]
        if (not path.startswith('node_modules/') or not expected
                or info['name'] != expected.get('name', name)
                or info['version'] != expected['version']):
            raise ValueError('bundled dashboard package differs from its locked identity')
        packages.add((info['name'], info['version']))
    return packages


def check_scan(report, image_id, alpine, go):
    if report['Metadata']['ImageID'] != image_id:
        raise ValueError('Trivy report is for a different image config digest')

    def packages(kind):
        return {(item['Name'], item['Version']) for result in report['Results']
                if result['Type'] == kind for item in result['Packages']}

    if not alpine or packages('alpine') != alpine:
        raise ValueError('Trivy and the image APK database disagree')
    if {item for item in packages('gobinary') if item[0] != MODULE} != go:
        raise ValueError('Trivy and the shipped Go binary disagree')


def document(digest, revision, inventories, created):
    doc_id = 'SPDXRef-DOCUMENT'
    image_id = 'SPDXRef-Cairn-Image'

    def package(identifier, name, version, purpose):
        return {'SPDXID': identifier, 'name': name, 'versionInfo': version,
                'primaryPackagePurpose': purpose, 'downloadLocation': 'NOASSERTION',
                'filesAnalyzed': False, 'licenseDeclared': 'NOASSERTION',
                'licenseConcluded': 'NOASSERTION', 'copyrightText': 'NOASSERTION'}

    def relationship(source, kind, target):
        return {'spdxElementId': source, 'relationshipType': kind, 'relatedSpdxElement': target}

    image = package(image_id, 'cairn-control-plane-linux-amd64', digest, 'CONTAINER')
    image['checksums'] = [{'algorithm': 'SHA256', 'checksumValue': digest.removeprefix('sha256:')}]
    image['sourceInfo'] = f'https://{MODULE}/commit/{revision}'
    image['comment'] = 'The SHA-256 identifies the exact Linux/amd64 OCI image manifest bytes.'
    packages = [image]
    relationships = [relationship(doc_id, 'DESCRIBES', image_id)]
    for family, identities in inventories.items():
        for index, (name, version) in enumerate(sorted(identities), start=1):
            identifier = f'SPDXRef-{family}-{index}'
            entry = package(identifier, name, version, 'LIBRARY')
            if family == 'Alpine':
                del entry['primaryPackagePurpose']
            entry['comment'] = {
                'Alpine': 'Installed Alpine package recorded by APK.',
                'Go': 'Go module or standard library embedded in the Cairn binary.',
                'Npm': 'npm package contributing rendered modules to the dashboard JavaScript.',
            }[family]
            packages.append(entry)
            relationships.append(relationship(image_id, 'CONTAINS', identifier))
    doc = {
        'SPDXID': doc_id, 'spdxVersion': 'SPDX-2.3', 'dataLicense': 'CC0-1.0',
        'name': 'Cairn Linux/amd64 ' + digest,
        'creationInfo': {'created': created, 'creators': ['Tool: cairn-image-sbom-1']},
        'packages': packages, 'relationships': relationships,
    }
    doc['documentNamespace'] = f'https://{MODULE}/spdx/oci/' + sha256(json.dumps(doc, sort_keys=True).encode())
    return doc


def inspect_image(digest, lock_bytes):
    """Read a stopped container only after matching its platform manifest."""
    container = subprocess.check_output(
        ['docker', 'create', '--pull', 'never', '--platform', 'linux/amd64', 'cairn:ci'], text=True,
    ).strip()
    if not re.fullmatch(r'[0-9a-f]{64}', container):
        raise ValueError('Docker did not return a container ID')
    try:
        [instance] = json.loads(subprocess.check_output(['docker', 'container', 'inspect', container]))
        if (instance.get('ImageManifestDescriptor') or {}).get('digest') != digest:
            raise ValueError('Docker selected a platform manifest different from the verified archive')
        with tempfile.TemporaryDirectory() as directory:
            data = Path(directory)
            (data / 'web').mkdir()
            for source, destination in (
                ('/lib/apk/db/installed', 'apk'),
                ('/usr/local/bin/cairn', 'cairn'),
                ('/usr/local/share/cairn/dashboard-inventory.json', 'dashboard.json'),
                ('/srv/cairn/web/.', 'web'),
            ):
                subprocess.run(['docker', 'cp', f'{container}:{source}', str(data / destination)], check=True)
            build_info = subprocess.check_output(['go', 'version', '-m', str(data / 'cairn')], text=True)
            evidence = json.loads((data / 'dashboard.json').read_text())
            return {
                'Alpine': alpine_packages(data / 'apk'),
                'Go': go_packages(build_info),
                'Npm': dashboard_packages(evidence, data / 'web', lock_bytes),
            }
    finally:
        subprocess.run(['docker', 'rm', container], check=True, stdout=subprocess.DEVNULL)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--revision', required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9a-f]{40}', args.revision):
        parser.error('expected a full Git revision')

    try:
        digest, image_id = image_identity(Path('cairn-image.tar'), args.revision)
        inventories = inspect_image(digest, Path('web/package-lock.json').read_bytes())
        report = json.loads(Path('image-vulnerabilities.json').read_text())
        check_scan(report, image_id, inventories['Alpine'], inventories['Go'])
        created = datetime.now(UTC).strftime('%Y-%m-%dT%H:%M:%SZ')
        sbom = document(digest, args.revision, inventories, created)
        with Path('cairn-linux-amd64.spdx.json').open('x') as output:
            output.write(json.dumps(sbom, indent=2, sort_keys=True) + '\n')
    except (OSError, ValueError, KeyError, tarfile.TarError, subprocess.CalledProcessError) as exc:
        parser.exit(1, f'SBOM verification failed: {exc}\n')
    print(f'Verified {digest} with ' + ', '.join(f'{len(items)} {kind}' for kind, items in inventories.items()))


if __name__ == '__main__':
    main()
