// Generates configs for isolated parser checks. This command never applies a host network change.
package main

import (
	"lab-router/internal/model"
	"lab-router/internal/network"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := "artifacts/generated"
	os.MkdirAll(dir, 0700)
	c := model.Default()
	for n, b := range map[string]string{"hostapd.conf": strings.ReplaceAll(network.Hostapd(c), network.Dir+"/deny.mac", filepath.Join(mustAbs(dir), "deny.mac")), "deny.mac": "", "dnsmasq.conf": network.Dnsmasq(c), "firewall.nft": network.Firewall(c, []string{"203.0.113.0/24"})} {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(b), 0600); e != nil {
			panic(e)
		}
	}
}

func mustAbs(p string) string {
	v, e := filepath.Abs(p)
	if e != nil {
		panic(e)
	}
	return v
}
