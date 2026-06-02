package rng

import (
	"hash/fnv"
	"math/rand/v2"
)

// Deriver builds independent random generators from one base seed and a set of keys.
// The stream for a given set of keys depends only on the seed and those keys,
// never on the order in which generators are created. This keeps a run reproducible
// even when many goroutines ask for their own generator at the same time.
type Deriver struct {
	seed uint64
}

// NewDeriver returns a deriver rooted at the given base seed.
func NewDeriver(seed uint64) *Deriver {
	return &Deriver{seed: seed}
}

// Rand returns a generator whose stream is fixed by the base seed and the keys.
// Pass keys like a request id and a backend id to get one stable stream per pair.
func (d *Deriver) Rand(keys ...uint64) *rand.Rand {
	mixed := mix(d.seed, keys...)
	return rand.New(rand.NewPCG(mixed, mixed^0x9e3779b97f4a7c15))
}

// HashKey turns a string id into a uint64 key so string ids can be used with Rand.
// It uses FNV, which is stable across runs, unlike the default map hash.
func HashKey(id string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(id))
	return h.Sum64()
}

func mix(seed uint64, keys ...uint64) uint64 {
	h := seed
	for _, k := range keys {
		h ^= k
		h = splitmix64(h)
	}
	return h
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
