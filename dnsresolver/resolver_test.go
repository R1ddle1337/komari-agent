package dnsresolver

import (
	"net/http"
	"testing"
	"time"
)

func TestVerifiedHTTPClientKeepsTLSVerificationIndependent(t *testing.T) {
	previous := flags.IgnoreUnsafeCert
	t.Cleanup(func() { flags.IgnoreUnsafeCert = previous })
	flags.IgnoreUnsafeCert = true
	panel := GetHTTPClient(time.Minute)
	verified := GetVerifiedHTTPClient(time.Minute)
	panelTransport := panel.Transport.(*http.Transport)
	verifiedTransport := verified.Transport.(*http.Transport)
	if !panelTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("panel certificate option was not preserved")
	}
	if verifiedTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("verified client disabled certificate verification")
	}
	if verifiedTransport.Proxy == nil || verifiedTransport.DialContext == nil {
		t.Fatal("verified client lost the configured proxy or DNS dialer")
	}
	if panel == verified || panelTransport == verifiedTransport {
		t.Fatal("panel and verified clients share mutable transport state")
	}
	flags.IgnoreUnsafeCert = false
	if GetVerifiedHTTPClient(time.Minute) != verified {
		t.Fatal("verified client should not depend on the panel certificate option")
	}
}
