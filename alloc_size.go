package goja

import "sort"

// allocSize is what Go takes to make n bytes of memory with no pointers
// in one allocation, which is what runtime.MemStats.TotalAlloc counts: a
// small object (up to 32 KiB) its size class, a large one whole 8 KiB
// pages. The charges of the allocations that BigInts and the messages
// that hold a value make are estimated with it: an estimate of n bytes,
// or of n plus an eighth, fell short of the class or the pages (a
// 28,685-byte text is made in 32,768, charged 32,286: 100 x.toString(32)
// of 2241 words made 146 KB more than they were charged).
func allocSize(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if n < 16 {
		// A tiny block, which the tiny allocator shares (and the race
		// detector's runtime does not).
		return 16
	}
	if n <= maxSmallAllocSize {
		i := sort.Search(len(allocSizeClasses), func(i int) bool { return int64(allocSizeClasses[i]) >= n })
		return int64(allocSizeClasses[i])
	}
	return (n + allocPageSize - 1) &^ (allocPageSize - 1)
}

const (
	maxSmallAllocSize = 32 << 10
	allocPageSize     = 8 << 10
)

// allocSizeClasses are Go's size classes but class 0, copied from
// internal/runtime/gc's SizeClassToSize of go1.27.3, the toolchain this
// fork is built with. TestAllocSizeClasses holds them to what the runtime
// of the toolchain at hand makes, and fails if they differ.
var allocSizeClasses = [...]uint16{8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528, 6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568, 14336, 16384, 18432, 19072, 20480, 21760, 24576, 27264, 28672, 32768}
