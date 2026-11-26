//go:build !vercel

package main

import (
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/gopxl/beep/v2"
)

func TestPCMReaderEncodesStereoSigned16LittleEndian(t *testing.T) {
	samples := [][2]float64{{1.5, -2}, {0.5, -0.5}}
	index := 0
	stream := beep.StreamerFunc(func(dst [][2]float64) (int, bool) {
		n := copy(dst, samples[index:])
		index += n
		return n, index < len(samples)
	})
	var doneErr error
	r := pcmReader{stream: stream, done: func(err error) { doneErr = err }}

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("Read = (%d, %v), want (%d, nil)", n, err, len(buf))
	}
	if got, want := hex.EncodeToString(buf), "ff7f0180ff3f01c0"; got != want {
		t.Fatalf("PCM = %s, want %s", got, want)
	}
	if _, err := r.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("final Read error = %v, want EOF", err)
	}
	if doneErr != nil {
		t.Fatalf("completion error = %v, want nil", doneErr)
	}
}

func TestPCMReaderRejectsUnalignedBuffer(t *testing.T) {
	r := pcmReader{stream: beep.StreamerFunc(func([][2]float64) (int, bool) {
		t.Fatal("unaligned read called streamer")
		return 0, false
	}), done: func(error) {}}
	if _, err := r.Read(make([]byte, 3)); err == nil {
		t.Fatal("unaligned PCM buffer was accepted")
	}
}
