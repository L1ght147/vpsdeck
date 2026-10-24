package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/L1ght147/vpsdeck/internal/report"
)

func TestSendPinsHubCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	good := hex.EncodeToString(sum[:])

	send := func(fp string) error {
		a := New(&Config{HubURL: srv.URL, Token: "t", Fingerprint: fp}, "test")
		return a.Send(context.Background(), report.Report{})
	}
	if err := send(good); err != nil {
		t.Fatalf("matching fingerprint rejected: %v", err)
	}
	if err := send("ZZ" + good[2:]); err == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	if err := send("00" + good[2:]); err == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	if err := send(""); err == nil {
		t.Fatal("self-signed certificate accepted without a pin")
	}
}
