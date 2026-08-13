package scanner

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
)

type ClamAV struct {
	address     string
	dialTimeout atomic.Int64
	scanTimeout atomic.Int64
	maximum     atomic.Int64
}

func New(options config.ScannerOptions) (*ClamAV, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create ClamAV scanner: %w", err)
	}
	scanner := &ClamAV{address: options.Address}
	scanner.SetLimits(options.DialTimeout, options.ScanTimeout, options.MaximumStream)
	return scanner, nil
}

func (s *ClamAV) Ping(ctx context.Context) error {
	connection, cancel, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = connection.Close() }()
	if _, err := connection.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("write ClamAV ping: %w", err)
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 64)).ReadString(0)
	if err != nil {
		return fmt.Errorf("read ClamAV ping: %w", err)
	}
	if strings.TrimRight(response, "\x00\r\n") != "PONG" {
		return errors.New("ping ClamAV: invalid response")
	}
	return nil
}

func (s *ClamAV) Scan(ctx context.Context, source io.Reader) (domain.ScanResult, error) {
	if source == nil {
		return domain.ScanResult{}, errors.New("scan object: source is required")
	}
	connection, cancel, err := s.dial(ctx)
	if err != nil {
		return domain.ScanResult{}, err
	}
	defer cancel()
	defer func() { _ = connection.Close() }()
	if _, err := connection.Write([]byte("zINSTREAM\x00")); err != nil {
		return domain.ScanResult{}, fmt.Errorf("start ClamAV stream: %w", err)
	}
	buffered := bufio.NewReader(source)
	prefix, _ := buffered.Peek(512)
	detectedType := http.DetectContentType(prefix)
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		read, readErr := buffered.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > s.maximum.Load() {
				return domain.ScanResult{}, errors.New("scan object: stream exceeds configured maximum")
			}
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(read))
			if _, err := connection.Write(size[:]); err != nil {
				return domain.ScanResult{}, fmt.Errorf("write ClamAV chunk size: %w", err)
			}
			if _, err := connection.Write(buffer[:read]); err != nil {
				return domain.ScanResult{}, fmt.Errorf("write ClamAV chunk: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return domain.ScanResult{}, fmt.Errorf("read object for ClamAV: %w", readErr)
		}
	}
	if _, err := connection.Write([]byte{0, 0, 0, 0}); err != nil {
		return domain.ScanResult{}, fmt.Errorf("finish ClamAV stream: %w", err)
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString(0)
	if err != nil {
		return domain.ScanResult{}, fmt.Errorf("read ClamAV result: %w", err)
	}
	response = strings.TrimRight(response, "\x00\r\n")
	switch {
	case strings.HasSuffix(response, ": OK"):
		return domain.ScanResult{Clean: true, DetectedType: detectedType}, nil
	case strings.HasSuffix(response, " FOUND"):
		return domain.ScanResult{Clean: false, DetectedType: detectedType}, nil
	default:
		return domain.ScanResult{}, errors.New("scan object: ClamAV returned an invalid response")
	}
}

func (s *ClamAV) dial(ctx context.Context) (net.Conn, context.CancelFunc, error) {
	if s == nil {
		return nil, func() {}, errors.New("connect ClamAV: scanner is nil")
	}
	operationCtx, cancel := context.WithTimeout(ctx, time.Duration(s.scanTimeout.Load()))
	connection, err := (&net.Dialer{Timeout: time.Duration(s.dialTimeout.Load())}).DialContext(operationCtx, "tcp", s.address)
	if err != nil {
		cancel()
		return nil, func() {}, fmt.Errorf("connect ClamAV: %w", err)
	}
	if deadline, ok := operationCtx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			_ = connection.Close()
			cancel()
			return nil, func() {}, fmt.Errorf("set ClamAV deadline: %w", err)
		}
	}
	return connection, cancel, nil
}

func (s *ClamAV) SetLimits(dialTimeout, scanTimeout time.Duration, maximum int64) {
	if s != nil {
		s.dialTimeout.Store(int64(dialTimeout))
		s.scanTimeout.Store(int64(scanTimeout))
		s.maximum.Store(maximum)
	}
}
