package claude

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var (
	uuidRE     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	deviceIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Identity holds the per-process Claude Code identity values.
type identity struct {
	stateDir func() (string, error)

	once     sync.Once
	device   string
	fallback string
	mu       sync.Mutex
}

func newIdentity(stateDir func() (string, error)) *identity { return &identity{stateDir: stateDir} }

// sessionID keeps a valid host session UUID and otherwise uses one process-wide UUID.
func (id *identity) sessionID(host string) string {
	if uuidRE.MatchString(host) {
		return host
	}
	id.mu.Lock()
	defer id.mu.Unlock()
	if id.fallback == "" {
		id.fallback = randomUUID()
	}
	return id.fallback
}

// deviceID reads or creates the private persistent device id once; disk failures fall back
// to a process-wide random id.
func (id *identity) deviceID() string {
	id.once.Do(func() {
		dir, err := id.stateDir()
		if err != nil {
			id.device = loadDevice("", "")
			return
		}
		id.device = loadDevice(filepath.Join(dir, "claude-device-id"), filepath.Join(filepath.Dir(dir), "pi-auth-plus", "claude-device-id"))
	})
	return id.device
}

// loadDevice adopts pi-auth-plus' device id from legacy when path does not exist yet, so the
// account keeps one Claude Code device across the switch.
func loadDevice(path, legacy string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	generated := hex.EncodeToString(b)
	if data, err := os.ReadFile(legacy); err == nil && deviceIDRE.Match(data) {
		generated = string(data)
	}
	read := func() (string, bool) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		if s := string(data); deviceIDRE.MatchString(s) {
			return s, true
		}
		return generated, true
	}
	if path == "" {
		return generated
	}
	if s, ok := read(); ok {
		return s
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return generated
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			if s, ok := read(); ok {
				return s
			}
		}
		return generated
	}
	_, werr := f.WriteString(generated)
	if cerr := f.Close(); werr != nil || cerr != nil {
		return generated
	}
	return generated
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
