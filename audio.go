//go:build !vercel && !server

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/ebitengine/oto/v3"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/flac"
	"github.com/gopxl/beep/v2/mp3"
)

const playbackSampleRate = 44100

type audioEngine struct {
	mu          sync.Mutex
	playbackMu  sync.Mutex
	context     *oto.Context
	initialized bool
	err         error
}

func (e *audioEngine) output() (*oto.Context, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.initialized {
		return e.context, e.err
	}

	e.initialized = true
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   playbackSampleRate,
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		e.err = err
		return nil, err
	}
	e.context = ctx
	<-ready
	e.err = ctx.Err()
	return e.context, e.err
}

func (e *audioEngine) open(ctx context.Context, c *client, t track, q quality, key string) (*audioSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.playbackMu.Lock()
	releasePlayback := true
	defer func() {
		if releasePlayback {
			e.playbackMu.Unlock()
		}
	}()
	output, err := e.output()
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	link, err := c.resolveContext(streamCtx, "kuwo", t, q)
	if err != nil {
		cancel()
		return nil, err
	}
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, link, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("audio stream: http %s", resp.Status)
	}

	var stream beep.StreamSeekCloser
	var format beep.Format
	switch strings.ToLower(q.Format) {
	case "flac":
		stream, format, err = flac.Decode(resp.Body)
	case "mp3":
		stream, format, err = mp3.Decode(resp.Body)
	default:
		err = fmt.Errorf("unsupported audio format %q", q.Format)
	}
	if err != nil {
		resp.Body.Close()
		cancel()
		return nil, err
	}
	if format.SampleRate <= 0 {
		stream.Close()
		cancel()
		return nil, errors.New("audio stream has an invalid sample rate")
	}

	session := &audioSession{
		key:    key,
		label:  label(t),
		cancel: cancel,
		stream: stream,
		done:   make(chan struct{}),
		release: func() {
			e.playbackMu.Unlock()
		},
	}
	releasePlayback = false
	pcm := &pcmReader{
		stream: beep.Resample(8, format.SampleRate, beep.SampleRate(playbackSampleRate), stream),
		done:   session.finish,
	}
	session.player = output.NewPlayer(pcm)
	if err := ctx.Err(); err != nil {
		session.stop()
		return nil, err
	}
	return session, nil
}

type audioSession struct {
	key       string
	label     string
	player    *oto.Player
	stream    beep.StreamSeekCloser
	cancel    context.CancelFunc
	release   func()
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	err       error
}

func (s *audioSession) finish(err error) {
	s.doneOnce.Do(func() {
		s.err = err
		close(s.done)
	})
}

func (s *audioSession) stop() {
	s.closeOnce.Do(func() {
		defer s.release()
		s.cancel()
		s.player.Close()
		s.stream.Close()
		s.finish(nil)
	})
}

type pcmReader struct {
	stream  beep.Streamer
	done    func(error)
	samples [][2]float64
	ended   bool
}

func (r *pcmReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(p)%4 != 0 {
		return 0, errors.New("unaligned PCM buffer")
	}
	if r.ended {
		r.done(r.stream.Err())
		return 0, io.EOF
	}

	count := len(p) / 4
	if cap(r.samples) < count {
		r.samples = make([][2]float64, count)
	} else {
		r.samples = r.samples[:count]
	}
	n, ok := r.stream.Stream(r.samples)
	if n == 0 && !ok {
		err := r.stream.Err()
		r.done(err)
		return 0, io.EOF
	}
	for i, sample := range r.samples[:n] {
		for channel, value := range sample {
			value = math.Max(-1, math.Min(1, value))
			binary.LittleEndian.PutUint16(p[(i*2+channel)*2:], uint16(int16(value*32767)))
		}
	}
	if !ok {
		r.ended = true
	}
	return n * 4, nil
}
