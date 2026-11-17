from contextlib import chdir, redirect_stderr, redirect_stdout
import gzip
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import image_sbom as sbom

REVISION = 'a' * 40


class ImageSbomTests(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)

    def archive(self, *, tamper=False, architecture='amd64', wrong_diff_id=False):
        members = {}

        def blob(payload, media_type):
            digest = sbom.sha256(payload)
            members['blobs/sha256/' + digest] = payload
            return {'digest': 'sha256:' + digest, 'size': len(payload), 'mediaType': media_type}

        raw_layer = b'layer'
        layer = blob(gzip.compress(raw_layer), 'application/vnd.oci.image.layer.v1.tar+gzip')
        diff_id = 'b' * 64 if wrong_diff_id else sbom.sha256(raw_layer)
        config = blob(json.dumps({
            'os': 'linux', 'architecture': architecture,
            'config': {'Labels': {'org.opencontainers.image.revision': REVISION}},
            'rootfs': {'diff_ids': ['sha256:' + diff_id]},
        }).encode(), 'application/vnd.oci.image.config.v1+json')
        manifest = blob(json.dumps({
            'schemaVersion': 2, 'config': config, 'layers': [layer],
        }).encode(), 'application/vnd.oci.image.manifest.v1+json')
        manifest['platform'] = {'os': 'linux', 'architecture': architecture}
        index = blob(json.dumps({'schemaVersion': 2, 'manifests': [manifest]}).encode(), sbom.OCI_INDEX)
        members['index.json'] = json.dumps({'schemaVersion': 2, 'manifests': [index]}).encode()
        if tamper:
            members['blobs/sha256/' + layer['digest'][7:]] = bytes(layer['size'])
        path = self.root / 'image.tar'
        with tarfile.open(path, 'w') as archive:
            for name, payload in members.items():
                entry = tarfile.TarInfo(name)
                entry.size = len(payload)
                archive.addfile(entry, io.BytesIO(payload))
        return path, manifest['digest'], config['digest']

    def test_selects_and_verifies_the_platform_manifest(self):
        path, digest, config = self.archive()
        self.assertEqual(sbom.image_identity(path, REVISION), (digest, config))
        with self.assertRaisesRegex(ValueError, 'source revision'):
            sbom.image_identity(path, 'b' * 40)

    def test_rejects_corrupted_layer_and_wrong_architecture(self):
        path, _, _ = self.archive(tamper=True)
        with self.assertRaisesRegex(ValueError, 'digest mismatch'):
            sbom.image_identity(path, REVISION)
        path, _, _ = self.archive(architecture='arm64')
        with self.assertRaisesRegex(ValueError, 'Linux/amd64'):
            sbom.image_identity(path, REVISION)
        path, _, _ = self.archive(wrong_diff_id=True)
        with self.assertRaisesRegex(ValueError, 'config root filesystem'):
            sbom.image_identity(path, REVISION)

    def test_apk_and_go_match_the_scanned_image(self):
        apk = self.root / 'installed'
        apk.write_text('P:git\nV:2.0-r1\n\nP:busybox\nV:1.0-r2\n')
        build = ('cairn: go1.26.11\n\tpath\t' + sbom.MODULE + '/cmd/cairn\n'
                 '\tdep\texample.org/x\tv1.2.3\th1:example\n'
                 '\tbuild\tGOOS=linux\n\tbuild\tGOARCH=amd64\n')
        alpine = sbom.alpine_packages(apk)
        go = sbom.go_packages(build)
        self.assertEqual(alpine, {('git', '2.0-r1'), ('busybox', '1.0-r2')})
        self.assertEqual(go, {('stdlib', 'v1.26.11'), ('example.org/x', 'v1.2.3')})
        image_id = 'sha256:' + 'b' * 64
        report = {'Metadata': {'ImageID': image_id}, 'Results': [
            {'Type': kind, 'Packages': [{'Name': n, 'Version': v} for n, v in records]}
            for kind, records in (('alpine', alpine), ('gobinary', go))
        ]}
        report['Results'][1]['Packages'].append({'Name': sbom.MODULE})
        sbom.check_scan(report, image_id, alpine, go)
        with self.assertRaisesRegex(ValueError, 'config digest'):
            sbom.check_scan(report, 'sha256:' + 'c' * 64, alpine, go)
        with self.assertRaisesRegex(ValueError, 'APK'):
            sbom.check_scan(report, image_id, {('git', '1.0-r1')}, go)
        with self.assertRaisesRegex(ValueError, 'Go binary'):
            sbom.check_scan(report, image_id, alpine, {('stdlib', 'v1.26.11')})
        with self.assertRaisesRegex(ValueError, 'unversioned or replaced'):
            sbom.go_packages(build + '\t=>\t./local\t(devel)\n')

    def test_checkout_cli_binds_the_archive_and_reconciles_the_scan(self):
        archive, digest, config = self.archive()
        archive.rename(self.root / 'cairn-checkout-image.tar')
        report = {'Metadata': {'ImageID': config}, 'Results': [
            {'Type': 'alpine', 'Packages': [{'Name': 'git', 'Version': '2.0-r1'}]},
        ]}
        report_path = self.root / 'checkout-image-vulnerabilities.json'
        output = self.root / 'cairn-checkout-linux-amd64.spdx.json'
        argv = ['image_sbom.py', '--revision', REVISION, '--checkout']
        installed = {'Alpine': {('git', '2.0-r1')}}
        for scanned_id, scanned_version, error in (
            (config, '2.0-r1', None),
            ('sha256:' + 'b' * 64, '2.0-r1', 'config digest'),
            (config, '2.0-r2', 'APK database'),
        ):
            with self.subTest(error=error):
                report['Metadata']['ImageID'] = scanned_id
                report['Results'][0]['Packages'][0]['Version'] = scanned_version
                report_path.write_text(json.dumps(report))
                with (chdir(self.root), patch('sys.argv', argv),
                      patch.object(sbom, 'inspect_checkout', return_value=installed) as inspect,
                      redirect_stdout(io.StringIO()), redirect_stderr(io.StringIO()) as stderr):
                    if error:
                        with self.assertRaises(SystemExit) as exit_status:
                            sbom.main()
                        self.assertEqual(exit_status.exception.code, 1)
                        self.assertIn(error, stderr.getvalue())
                    else:
                        sbom.main()
                inspect.assert_called_once_with(digest)
                if error:
                    self.assertFalse(output.exists())
                    continue
                doc = json.loads(output.read_text())
                self.assertEqual(doc['spdxVersion'], 'SPDX-2.3')
                image, git = doc['packages']
                self.assertEqual(image['SPDXID'], 'SPDXRef-Cairn-Checkout-Image')
                self.assertEqual(image['versionInfo'], digest)
                self.assertEqual(image['checksums'], [{'algorithm': 'SHA256', 'checksumValue': digest[7:]}])
                self.assertTrue(image['sourceInfo'].endswith(REVISION))
                self.assertEqual((git['name'], git['versionInfo']), ('git', '2.0-r1'))
                output.unlink()

    def test_checkout_refuses_to_copy_from_a_different_manifest(self):
        container = 'a' * 64
        inspection = [{'ImageManifestDescriptor': {'digest': 'sha256:' + 'b' * 64}}]
        with (patch.object(sbom.subprocess, 'check_output', side_effect=[container, json.dumps(inspection)]) as check,
              patch.object(sbom.subprocess, 'run') as run):
            with self.assertRaisesRegex(ValueError, 'different from the verified archive'):
                sbom.inspect_checkout('sha256:' + 'c' * 64)
            self.assertEqual(check.call_args_list[0].args[0][-1], 'cairn-checkout:ci')
            run.assert_called_once_with(['docker', 'rm', container], check=True, stdout=sbom.subprocess.DEVNULL)

    def test_only_rendered_locked_packages_and_exact_dashboard_files(self):
        lock = json.dumps({'packages': {
            'node_modules/react': {'version': '18.3.1'},
            'node_modules/vitest': {'version': '4.1.12'},
        }}).encode()
        web = self.root
        files = {'index.html': b'<html>', 'index.js': b'render();'}
        for name, data in files.items():
            (web / name).write_bytes(data)
        evidence = {'schema': 1, 'lock_sha256': sbom.sha256(lock),
                    'files': {name: sbom.sha256(raw) for name, raw in files.items()},
                    'chunks': {'index.js': ['node_modules/react']},
                    'packages': {'node_modules/react': {'name': 'react', 'version': '18.3.1'}}}
        self.assertEqual(sbom.dashboard_packages(evidence, web, lock), {('react', '18.3.1')})
        evidence['packages']['node_modules/react']['version'] = '18.3.0'
        with self.assertRaisesRegex(ValueError, 'locked identity'):
            sbom.dashboard_packages(evidence, web, lock)
        evidence['packages']['node_modules/react']['version'] = '18.3.1'
        (web / 'index.js').write_bytes(b'other();')
        with self.assertRaisesRegex(ValueError, 'file hashes'):
            sbom.dashboard_packages(evidence, web, lock)

    def test_document_binds_digest_and_keeps_package_families_distinct(self):
        digest = 'sha256:' + 'c' * 64
        inventories = {group: {('example', '1.2')} for group in ('Alpine', 'Go', 'Npm')}
        doc = sbom.document(digest, REVISION, inventories, '2026-11-16T20:00:00Z')
        self.assertEqual(doc['spdxVersion'], 'SPDX-2.3')
        self.assertEqual(doc['packages'][0]['checksums'], [{'algorithm': 'SHA256', 'checksumValue': digest[7:]}])
        self.assertTrue(doc['packages'][0]['sourceInfo'].endswith(REVISION))
        ids = {p['SPDXID'] for p in doc['packages']}
        self.assertEqual(ids, {'SPDXRef-Cairn-Image', 'SPDXRef-Alpine-1', 'SPDXRef-Go-1', 'SPDXRef-Npm-1'})
        self.assertEqual({r['relatedSpdxElement'] for r in doc['relationships']}, ids)


if __name__ == '__main__':
    unittest.main()
