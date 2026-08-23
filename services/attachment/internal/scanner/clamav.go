package scanner

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Clean        bool
	SHA256       string
	Size         int64
	DetectedType string
}
type ClamAV struct {
	address                  string
	dialTimeout, scanTimeout time.Duration
	maximum                  int64
}

func New(address string, dial, scan time.Duration, max int64) *ClamAV {
	return &ClamAV{address: address, dialTimeout: dial, scanTimeout: scan, maximum: max}
}
func (s *ClamAV) Ping(ctx context.Context) error {
	c, cancel, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = c.Close() }()
	if _, err = c.Write([]byte("zPING\x00")); err != nil {
		return err
	}
	r, err := bufio.NewReader(io.LimitReader(c, 64)).ReadString(0)
	if err != nil {
		return err
	}
	if strings.TrimRight(r, "\x00\r\n") != "PONG" {
		return errors.New("invalid ClamAV ping")
	}
	return nil
}
func (s *ClamAV) Scan(ctx context.Context, source io.Reader) (Result, error) {
	if source == nil {
		return Result{}, errors.New("scan source is required")
	}
	c, cancel, err := s.dial(ctx)
	if err != nil {
		return Result{}, err
	}
	defer cancel()
	defer func() { _ = c.Close() }()
	if _, err = c.Write([]byte("zINSTREAM\x00")); err != nil {
		return Result{}, err
	}
	br := bufio.NewReader(source)
	prefix, _ := br.Peek(512)
	detected := http.DetectContentType(prefix)
	hash := sha256.New()
	buf := make([]byte, 256<<10)
	var total int64
	for {
		n, readErr := br.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > s.maximum {
				return Result{}, errors.New("attachment exceeds scanner limit")
			}
			_, _ = hash.Write(buf[:n])
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(n))
			if _, err = c.Write(size[:]); err != nil {
				return Result{}, err
			}
			if _, err = c.Write(buf[:n]); err != nil {
				return Result{}, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Result{}, readErr
		}
	}
	if _, err = c.Write([]byte{0, 0, 0, 0}); err != nil {
		return Result{}, err
	}
	response, err := bufio.NewReader(io.LimitReader(c, 4096)).ReadString(0)
	if err != nil {
		return Result{}, err
	}
	response = strings.TrimRight(response, "\x00\r\n")
	clean := strings.HasSuffix(response, ": OK")
	if !clean && !strings.HasSuffix(response, " FOUND") {
		return Result{}, fmt.Errorf("invalid ClamAV result")
	}
	return Result{Clean: clean, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: total, DetectedType: detected}, nil
}
func (s *ClamAV) dial(ctx context.Context) (net.Conn, context.CancelFunc, error) {
	op, cancel := context.WithTimeout(ctx, s.scanTimeout)
	c, err := (&net.Dialer{Timeout: s.dialTimeout}).DialContext(op, "tcp", s.address)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	if deadline, ok := op.Deadline(); ok {
		_ = c.SetDeadline(deadline)
	}
	return c, cancel, nil
}
