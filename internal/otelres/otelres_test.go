package otelres

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/AndreiD108/d108-go/identity"
	"github.com/AndreiD108/d108-go/logging"
)

func TestResourceCarriesTheIdentity(t *testing.T) {
	id := identity.New("restest")
	res := New(id)
	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.Emit()
	}
	for k, v := range map[string]string{
		"service.name":        "restest",
		"service.version":     id.Version,
		"service.instance.id": id.InstanceID,
	} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["process.pid"] == "" || got["telemetry.sdk.language"] != "go" {
		t.Errorf("pid %q sdk %q", got["process.pid"], got["telemetry.sdk.language"])
	}
}

func TestEnabledFollowsTheEndpointVariable(t *testing.T) {
	t.Setenv(EndpointVar, "")
	if Enabled() {
		t.Error("enabled with the variable empty")
	}
	t.Setenv(EndpointVar, "http://127.0.0.1:4317")
	if !Enabled() {
		t.Error("disabled with the variable set")
	}
}

func TestErrorHandlerWritesWarnLine(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	var buf bytes.Buffer
	log := logging.New(identity.New("restest"), logging.Writer(&buf))
	prev := otel.GetErrorHandler()
	t.Cleanup(func() { otel.SetErrorHandler(prev) })
	SetErrorHandler(logging.NewContext(context.Background(), log))
	otel.Handle(errors.New("export failed"))
	line := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte(`"severity_text":"WARN"`)) || !bytes.Contains(buf.Bytes(), []byte(`"exception.message":"export failed"`)) {
		t.Errorf("line: %s", line)
	}
}
