package chathub

import "testing"

// A write timeout is a different fault from a read timeout and must not be
// reported as one.
func TestClassifyTransportErrorDistinguishesWriteTimeout(t *testing.T) {
	write := classifyTransportError(errText("chat send: write tcp 10.0.0.1:1->2.2.2.2:443: i/o timeout"))
	if write != "WS_WRITE_TIMEOUT" {
		t.Fatalf("write timeout classified as %q; want WS_WRITE_TIMEOUT", write)
	}
	read := classifyTransportError(errText("ws read before completion: i/o timeout"))
	if read != "WS_READ_TIMEOUT" {
		t.Fatalf("read timeout classified as %q; want WS_READ_TIMEOUT", read)
	}
}

type errText string

func (e errText) Error() string { return string(e) }