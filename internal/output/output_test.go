package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSuccess_EmitsVersionedEnvelopeToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr, FormatJSON)

	type payload struct {
		Name string `json:"name"`
	}
	if err := w.Success(payload{Name: "alice"}, nil); err != nil {
		t.Fatalf("Success returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty on success, got %q", stderr.String())
	}

	var got struct {
		V    int     `json:"v"`
		Data payload `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.V != Version {
		t.Errorf("v = %d, want %d", got.V, Version)
	}
	if got.Data.Name != "alice" {
		t.Errorf("data.name = %q, want %q", got.Data.Name, "alice")
	}
}

func TestSuccess_TableFormatNotImplemented(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr, FormatTable)

	err := w.Success("anything", nil)
	if err == nil {
		t.Fatal("expected error for table format, got nil")
	}
	if !strings.Contains(err.Error(), "table output") {
		t.Errorf("error message should mention table output, got %q", err)
	}
}

func TestFailure_EmitsVersionedEnvelopeToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr, FormatJSON)

	f := Errorf(CodeAuthRefreshFailed, "refresh failed for %s", "gmail-personal").
		WithDetails("account", "gmail-personal")

	if err := w.Failure(f); err != nil {
		t.Fatalf("Failure returned error: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on failure, got %q", stdout.String())
	}

	var got struct {
		V     int     `json:"v"`
		Error Failure `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &got); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\n%s", err, stderr.String())
	}
	if got.V != Version {
		t.Errorf("v = %d, want %d", got.V, Version)
	}
	if got.Error.Code != CodeAuthRefreshFailed {
		t.Errorf("code = %q, want %q", got.Error.Code, CodeAuthRefreshFailed)
	}
	if got.Error.Message != "refresh failed for gmail-personal" {
		t.Errorf("message = %q", got.Error.Message)
	}
	if got.Error.Details["account"] != "gmail-personal" {
		t.Errorf("details.account = %v", got.Error.Details["account"])
	}
}

func TestAsFailure(t *testing.T) {
	t.Run("unwraps a Failure", func(t *testing.T) {
		orig := Errorf(CodeConfigInvalid, "bad")
		got := AsFailure(orig)
		if got.Code != CodeConfigInvalid {
			t.Errorf("code = %q, want %q", got.Code, CodeConfigInvalid)
		}
	})
	t.Run("wraps a non-Failure as generic", func(t *testing.T) {
		got := AsFailure(errors.New("plain"))
		if got.Code != CodeGeneric {
			t.Errorf("code = %q, want %q", got.Code, CodeGeneric)
		}
		if got.Message != "plain" {
			t.Errorf("message = %q, want %q", got.Message, "plain")
		}
	})
}
