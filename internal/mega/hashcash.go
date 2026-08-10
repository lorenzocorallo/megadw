package mega

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hashcashReplications = 262144
	hashcashTokenSlot    = 48
)

type hashcashChallenge struct {
	easiness int
	token    string
}

func parseHashcashChallenge(header string) (hashcashChallenge, bool) {
	parts := strings.Split(header, ":")
	if len(parts) != 4 || parts[0] != "1" {
		return hashcashChallenge{}, false
	}
	easiness, err := strconv.Atoi(parts[1])
	if err != nil || easiness < 0 || easiness > 255 || parts[3] == "" {
		return hashcashChallenge{}, false
	}
	token, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(token) == 0 || len(token) > hashcashTokenSlot {
		return hashcashChallenge{}, false
	}
	return hashcashChallenge{easiness: easiness, token: parts[3]}, true
}

// solveHashcash is the small context-aware portion of the upstream hashcash
// algorithm needed by MEGA authentication. It intentionally has a hard
// deadline and a bounded worker count; it never launches a lifecycle that
// outlives the request that owns it.
func solveHashcash(ctx context.Context, challenge hashcashChallenge, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	workCtx, cancel := context.WithTimeout(ctx, timeout)

	workers := runtime.GOMAXPROCS(0) / 2
	if workers < 1 {
		workers = 1
	}
	if workers > 2 {
		workers = 2
	}
	result := make(chan string, 1)
	var workersWG sync.WaitGroup
	workersWG.Add(workers)
	defer func() {
		cancel()
		workersWG.Wait()
	}()
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer workersWG.Done()
			if value := generateHashcash(workCtx, challenge.token, challenge.easiness, uint32(worker+1), uint32(workers)); value != "" {
				select {
				case result <- value:
				case <-workCtx.Done():
				}
			}
		}(worker)
	}
	select {
	case value := <-result:
		return value, nil
	case <-workCtx.Done():
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", workCtx.Err()
	}
}

func generateHashcash(ctx context.Context, token string, easiness int, prefix, stride uint32) string {
	if strings.ContainsAny(token, "+/=") {
		return ""
	}
	tokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ""
	}
	if remainder := len(tokenBytes) % 16; remainder != 0 {
		tokenBytes = append(tokenBytes, make([]byte, 16-remainder)...)
	}
	threshold := uint32((((easiness & 63) << 1) + 1) << ((easiness>>6)*7 + 3))
	buffer := make([]byte, 4+hashcashReplications*hashcashTokenSlot)
	for index := 0; index < hashcashReplications; index++ {
		copy(buffer[4+index*hashcashTokenSlot:], tokenBytes)
	}
	if stride == 0 {
		stride = 1
	}
	for {
		select {
		case <-ctx.Done():
			return ""
		default:
		}
		binary.LittleEndian.PutUint32(buffer[:4], prefix)
		digest := sha256.Sum256(buffer)
		if binary.BigEndian.Uint32(digest[:4]) <= threshold {
			return base64.RawURLEncoding.EncodeToString(buffer[:4])
		}
		next := prefix + stride
		if next < prefix {
			return ""
		}
		prefix = next
	}
}

func hashcashHeader(challenge hashcashChallenge, solution string) string {
	return fmt.Sprintf("1:%s:%s", challenge.token, solution)
}
