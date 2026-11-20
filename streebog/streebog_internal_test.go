package streebog

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestLPTableMatchesLinearLP checks the table built by linearity against
// linearLP for every single-byte input, the inputs v0.1.0 built its table
// from: byte v at LE position 8j+k must come out as lpTable[j][v] in word k
// and as zero in every other word. That checks both the build and the
// transposition lps relies on.
func TestLPTableMatchesLinearLP(t *testing.T) {
	t.Parallel()

	tbl := new([8][256]uint64)

	// Start from garbage: buildLPTable must not rely on a zeroed table.
	for j := range tbl {
		for v := range tbl[j] {
			tbl[j][v] = 0xa5a5a5a5a5a5a5a5
		}
	}

	buildLPTable(tbl)

	for p := range 64 {
		j, k := p/bytesPerWord, p%bytesPerWord

		for v := range 256 {
			var in, want [64]byte

			in[p] = byte(v)
			binary.LittleEndian.PutUint64(want[8*k:], tbl[j][v])

			if got := linearLP(in); got != want {
				t.Fatalf("LP(%#02x at byte %d) = %x, want lpTable[%d][%#02x] in word %d: %x",
					v, p, got, j, v, k, want)
			}
		}
	}

	initTables()

	if lpTable != *tbl {
		t.Fatal("lpTable built by initTables differs from buildLPTable")
	}

	for i := range roundConstants {
		if want := bytesToWords(roundConstants[i]); roundConstantWords[i] != want {
			t.Fatalf("roundConstantWords[%d] = %x, want %x", i, roundConstantWords[i], want)
		}
	}
}

// TestLPSMatchesLinearLP checks lps against S applied byte by byte followed
// by linearLP, on random states.
func TestLPSMatchesLinearLP(t *testing.T) {
	t.Parallel()

	initTables()

	rng := rand.New(rand.NewPCG(0x5354524545424f47, 1)) //nolint:gosec // Test inputs, not keys.

	for range 10000 {
		var x [8]uint64

		for i := range x {
			x[i] = rng.Uint64()
		}

		in := wordsToBytes(x)
		for i := range in {
			in[i] = pi[in[i]]
		}

		if got, want := lps(x), bytesToWords(linearLP(in)); got != want {
			t.Fatalf("lps(%x) = %x, want %x", x, got, want)
		}
	}
}

// firstUseEnv marks the child process of TestFirstUseConcurrent.
const firstUseEnv = "STREEBOG_TEST_FIRST_USE"

// TestFirstUseConcurrent hashes through every entry point from many
// goroutines at once, as the first use of the package in the process. In this
// process other tests may have hashed already, so the check runs in a fresh
// copy of the test binary that runs only this test. Under -race the copy is
// race-enabled too.
func TestFirstUseConcurrent(t *testing.T) {
	t.Parallel()

	if os.Getenv(firstUseEnv) == "1" {
		firstUseConcurrent(t)

		return
	}

	cmd := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run=^TestFirstUseConcurrent$", "-test.count=1", "-test.v")

	cmd.Env = append(os.Environ(), firstUseEnv+"=1")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first-use child failed: %v\n%s", err, out)
	}

	if !strings.Contains(string(out), "--- PASS: TestFirstUseConcurrent") {
		t.Fatalf("first-use child did not run the test:\n%s", out)
	}
}

func firstUseConcurrent(t *testing.T) {
	t.Helper()

	// Only this test runs here, so nothing has hashed yet. Filled tables mean
	// package initialisation built them.
	if lpTable[0][1] != 0 || roundConstantWords[0] != ([8]uint64{}) {
		t.Fatal("tables are built before the first hash")
	}

	// M1 of RFC 6986 §10.1.
	msg := []byte("012345678901234567890123456789012345678901234567890123456789012")
	want256, _ := hex.DecodeString("9d151eefd8590b89daa6ba6cb74af9275dd051026bb149a452fd84e5e57b5500")
	want512, _ := hex.DecodeString("1b54d01a4af5b9d5cc3d86d68d285462b19abc2475222f35c085122be4ba1ffa" +
		"00ad30f8767b3a82384c6574f024c311e2a481332b08ef7f41797891c1646f48")

	viaHash := func(h hash.Hash) []byte {
		h.Write(msg)

		return h.Sum(nil)
	}

	entries := []struct {
		name string
		sum  func() []byte
		want []byte
	}{
		{"Sum256", func() []byte { d := Sum256(msg); return d[:] }, want256},
		{"Sum512", func() []byte { d := Sum512(msg); return d[:] }, want512},
		{"New256", func() []byte { return viaHash(New256()) }, want256},
		{"New512", func() []byte { return viaHash(New512()) }, want512},
	}

	const goroutines = 64

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make(chan error, goroutines)
	)

	for i := range goroutines {
		e := entries[i%len(entries)]

		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			if got := e.sum(); !bytes.Equal(got, e.want) {
				errs <- fmt.Errorf("%s: got %x, want %x", e.name, got, e.want)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

// BenchmarkLPTable measures building lpTable, the work the first hash in a
// process pays for.
func BenchmarkLPTable(b *testing.B) {
	tbl := new([8][256]uint64)

	b.SetBytes(int64(len(tbl) * len(tbl[0]) * bytesPerWord))
	b.ResetTimer()

	for range b.N {
		buildLPTable(tbl)
	}
}
