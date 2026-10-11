// SPDX-License-Identifier: AGPL-3.0-or-later
// A loopback-only synthetic LMS for Cairn's LTI/AGS rehearsal.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/internal/ltisim"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	dir := flag.String("dir", "", "new private directory for synthetic keys/config")
	port := flag.Int("port", 8097, "loopback simulator port")
	tool := flag.String("cairn-url", "http://127.0.0.1:8080", "Cairn loopback origin")
	classroom := flag.String("classroom", "", "existing synthetic Cairn classroom ID")
	flag.Parse()
	if *dir == "" || *classroom == "" {
		log.Fatal("--dir (new directory) and --classroom are required")
	}
	path, e := filepath.Abs(*dir)
	if e != nil {
		log.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		log.Fatal("choose a new evidence directory: ", e)
	}
	platform, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		log.Fatal(e)
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		log.Fatal(e)
	}
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if e != nil {
		log.Fatal(e)
	}
	base := "http://" + listener.Addr().String()
	sim, e := ltisim.New(base, *tool, platform, &key.PublicKey)
	if e != nil {
		log.Fatal(e)
	}
	keyFile := filepath.Join(path, "tool-key.pem")
	if e = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); e != nil {
		log.Fatal(e)
	}
	config, _ := json.MarshalIndent(sim.Config(*classroom, keyFile), "", "  ")
	cfgFile := filepath.Join(path, "lti.json")
	if e = os.WriteFile(cfgFile, append(config, '\n'), 0600); e != nil {
		log.Fatal(e)
	}
	fmt.Printf("Synthetic LMS: %s\nCairn configuration: CAIRN_LTI_CONFIG=%s\nRestarting the simulator resets its grades and platform signing key. No university data or accounts.\n", base, cfgFile)
	server := &http.Server{Handler: sim.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	log.Fatal(server.Serve(listener))
}
