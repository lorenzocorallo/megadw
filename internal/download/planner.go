package download

import (
	"errors"
	"fmt"
	"math"
)

const (
	// DefaultSegmentSize is the fixed range size used by new download files.
	DefaultSegmentSize int64 = 8 << 20
	// MaxBitmapBytes prevents malformed metadata from allocating an unbounded
	// resume bitmap. Multi-terabyte files at the default segment size are far
	// below this limit while a hostile minimum-size plan cannot consume the
	// application's entire memory budget.
	MaxBitmapBytes int64 = 128 << 20
)

var (
	ErrInvalidSegmentPlan = errors.New("invalid segment plan")
	ErrBitmapTooLarge     = errors.New("resume bitmap is too large")
	ErrSegmentIndex       = errors.New("segment index is out of range")
)

// Segment is one inclusive plaintext byte range in a download file.
type Segment struct {
	Index int64
	Start int64
	End   int64
}

// Size returns the number of bytes in the segment.
func (s Segment) Size() int64 {
	if s.End < s.Start {
		return 0
	}
	return s.End - s.Start + 1
}

// SegmentPlanner describes a file without materializing every segment. This
// keeps planning bounded even for multi-terabyte files.
type SegmentPlanner struct {
	Size        int64
	SegmentSize int64
	Count       int64
}

// Planner is the short compatibility name used by callers that do not need
// to distinguish this bounded planner from a future queue scheduler.
type Planner = SegmentPlanner

// NewPlanner is an alias for NewSegmentPlanner.
func NewPlanner(size, segmentSize int64) (SegmentPlanner, error) {
	return NewSegmentPlanner(size, segmentSize)
}

// NewSegmentPlanner creates a fixed-size range planner.
func NewSegmentPlanner(size, segmentSize int64) (SegmentPlanner, error) {
	if size < 0 {
		return SegmentPlanner{}, fmt.Errorf("%w: file size must not be negative", ErrInvalidSegmentPlan)
	}
	if segmentSize <= 0 {
		return SegmentPlanner{}, fmt.Errorf("%w: segment size must be positive", ErrInvalidSegmentPlan)
	}
	count := int64(0)
	if size > 0 {
		count = (size-1)/segmentSize + 1
	}
	return SegmentPlanner{Size: size, SegmentSize: segmentSize, Count: count}, nil
}

// Segment returns the range at index without allocating.
func (p SegmentPlanner) Segment(index int64) (Segment, error) {
	if index < 0 || index >= p.Count {
		return Segment{}, fmt.Errorf("%w: %d", ErrSegmentIndex, index)
	}
	start := index * p.SegmentSize
	// The validated count makes this multiplication safe for practical files;
	// keep the explicit check for callers constructing a planner value.
	if start < 0 || start > math.MaxInt64-p.SegmentSize {
		return Segment{}, fmt.Errorf("%w: segment start overflows int64", ErrInvalidSegmentPlan)
	}
	end := start + p.SegmentSize - 1
	if end >= p.Size {
		end = p.Size - 1
	}
	return Segment{Index: index, Start: start, End: end}, nil
}

// Pending returns unfinished segments in ascending order. It is intended for
// bounded files used by callers that need a slice; transfer code uses Segment
// directly so it never has to allocate the full queue.
func (p SegmentPlanner) Pending(bitmap Bitmap) ([]Segment, error) {
	if err := bitmap.Validate(p.Count); err != nil {
		return nil, err
	}
	if p.Count > int64(maxInt()) {
		return nil, fmt.Errorf("%w: segment count does not fit in memory", ErrInvalidSegmentPlan)
	}
	segments := make([]Segment, 0, p.Count)
	for index := int64(0); index < p.Count; index++ {
		if bitmap.IsSet(index) {
			continue
		}
		segment, err := p.Segment(index)
		if err != nil {
			return nil, err
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

// PlanSegments is a convenience for small callers and tests. The planner
// itself remains the bounded API for large files.
func PlanSegments(size, segmentSize int64) ([]Segment, error) {
	planner, err := NewSegmentPlanner(size, segmentSize)
	if err != nil {
		return nil, err
	}
	return planner.Pending(NewBitmapUnchecked(planner.Count))
}

// Bitmap is the compact durable representation of completed segments.
type Bitmap []byte

// Bitset is an alias retained for code that describes the persisted value by
// its representation rather than its download meaning.
type Bitset = Bitmap

// NewBitmap allocates exactly enough bytes for segmentCount bits.
func NewBitmap(segmentCount int64) (Bitmap, error) {
	if segmentCount < 0 {
		return nil, fmt.Errorf("%w: negative segment count", ErrInvalidSegmentPlan)
	}
	byteCount, err := bitmapByteCount(segmentCount)
	if err != nil {
		return nil, err
	}
	return make(Bitmap, byteCount), nil
}

// NewBitmapUnchecked is useful after a planner has already validated its
// count. It returns an empty bitmap rather than panicking on hostile values.
func NewBitmapUnchecked(segmentCount int64) Bitmap {
	bitmap, err := NewBitmap(segmentCount)
	if err != nil {
		return nil
	}
	return bitmap
}

func bitmapByteCount(segmentCount int64) (int, error) {
	if segmentCount == 0 {
		return 0, nil
	}
	bytes := (segmentCount-1)/8 + 1
	if bytes > MaxBitmapBytes || bytes > int64(maxInt()) {
		return 0, fmt.Errorf("%w: %d bytes", ErrBitmapTooLarge, bytes)
	}
	return int(bytes), nil
}

// Validate checks both the bitmap length and unused high bits.
func (b Bitmap) Validate(segmentCount int64) error {
	byteCount, err := bitmapByteCount(segmentCount)
	if err != nil {
		return err
	}
	if len(b) != byteCount {
		return fmt.Errorf("%w: bitmap length %d, want %d", ErrInvalidSegmentPlan, len(b), byteCount)
	}
	if segmentCount == 0 {
		return nil
	}
	usedBits := uint(segmentCount % 8)
	if usedBits != 0 && b[len(b)-1]&^byte((1<<usedBits)-1) != 0 {
		return fmt.Errorf("%w: bitmap has bits beyond segment count", ErrInvalidSegmentPlan)
	}
	return nil
}

// IsSet reports whether a segment has been durably or optimistically marked.
func (b Bitmap) IsSet(index int64) bool {
	if index < 0 || index/8 >= int64(len(b)) {
		return false
	}
	return b[index/8]&(1<<uint(index%8)) != 0
}

// Set marks one segment complete.
func (b Bitmap) Set(index int64) error {
	if index < 0 || index/8 >= int64(len(b)) {
		return fmt.Errorf("%w: %d", ErrSegmentIndex, index)
	}
	b[index/8] |= 1 << uint(index%8)
	return nil
}

// Clear removes one segment mark. It is used when recovering a partial file
// whose durable data was removed outside the downloader.
func (b Bitmap) Clear(index int64) error {
	if index < 0 || index/8 >= int64(len(b)) {
		return fmt.Errorf("%w: %d", ErrSegmentIndex, index)
	}
	b[index/8] &^= 1 << uint(index%8)
	return nil
}

// Count returns the number of marked bits.
func (b Bitmap) Count() int64 {
	var count int64
	for _, value := range b {
		count += int64(popcount8(value))
	}
	return count
}

// Clone returns an independent bitmap suitable for a database snapshot.
func (b Bitmap) Clone() Bitmap {
	return append(Bitmap(nil), b...)
}

// Bytes returns a detached raw representation suitable for a BLOB column.
func (b Bitmap) Bytes() []byte {
	return []byte(b.Clone())
}

// MarshalBinary implements encoding.BinaryMarshaler without adding framing;
// the segment count is stored in the owning download_files row.
func (b Bitmap) MarshalBinary() ([]byte, error) {
	return b.Bytes(), nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler. Call Validate after
// decoding when the owning segment count is known.
func (b *Bitmap) UnmarshalBinary(data []byte) error {
	if b == nil {
		return fmt.Errorf("nil bitmap receiver")
	}
	if int64(len(data)) > MaxBitmapBytes {
		return fmt.Errorf("%w: %d bytes", ErrBitmapTooLarge, len(data))
	}
	*b = append((*b)[:0], data...)
	return nil
}

// NewBitset is an alias for NewBitmap.
func NewBitset(segmentCount int64) (Bitmap, error) {
	return NewBitmap(segmentCount)
}

// DecodeBitmap copies and validates a serialized bitmap for a known plan.
func DecodeBitmap(data []byte, segmentCount int64) (Bitmap, error) {
	bitmap := Bitmap(append([]byte(nil), data...))
	if err := bitmap.Validate(segmentCount); err != nil {
		return nil, err
	}
	return bitmap, nil
}

func popcount8(value byte) int {
	count := 0
	for value != 0 {
		value &= value - 1
		count++
	}
	return count
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
