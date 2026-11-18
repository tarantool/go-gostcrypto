package streebog

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestLPTableMatchesLinearLP checks every entry of the table built by
// linearity against linearLP itself, the way v0.1.0 built each of them.
func TestLPTableMatchesLinearLP(t *testing.T) {
	t.Parallel()

	tbl := new([64][256][8]uint64)

	// Start from garbage: buildLPTable must not rely on a zeroed table.
	for p := range tbl {
		for v := range tbl[p] {
			for w := range tbl[p][v] {
				tbl[p][v][w] = 0xa5a5a5a5a5a5a5a5
			}
		}
	}

	buildLPTable(tbl)

	for p := range 64 {
		for v := range 256 {
			var in [64]byte

			in[p] = byte(v)

			if want := bytesToWords(linearLP(in)); tbl[p][v] != want {
				t.Fatalf("lpTable[%d][%#02x] = %x, want %x", p, v, tbl[p][v], want)
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
	if lpTable[0][1] != ([8]uint64{}) || roundConstantWords[0] != ([8]uint64{}) {
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
	tbl := new([64][256][8]uint64)

	b.SetBytes(int64(len(tbl) * len(tbl[0]) * len(tbl[0][0]) * bytesPerWord))
	b.ResetTimer()

	for range b.N {
		buildLPTable(tbl)
	}
}
