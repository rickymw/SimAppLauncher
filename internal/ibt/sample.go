package ibt

import (
	"encoding/binary"
	"math"
)

// Sample holds the raw bytes of one telemetry data row and a reference to
// the variable metadata needed to decode values from it.
type Sample struct {
	raw  []byte
	vars map[string]*VarDef
}

// Float32 returns the float32 value of the named variable.
// Returns (0, false) if the variable does not exist or has a different type.
// For array variables (Count > 1), this returns the first element.
func (s Sample) Float32(name string) (float32, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeFloat {
		return 0, false
	}
	if vd.Offset+4 > len(s.raw) {
		return 0, false
	}
	bits := binary.LittleEndian.Uint32(s.raw[vd.Offset:])
	return math.Float32frombits(bits), true
}

// Float64 returns the float64 value of the named variable.
// Returns (0, false) if the variable does not exist or has a different type.
// For array variables (Count > 1), this returns the first element.
func (s Sample) Float64(name string) (float64, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeDouble {
		return 0, false
	}
	if vd.Offset+8 > len(s.raw) {
		return 0, false
	}
	bits := binary.LittleEndian.Uint64(s.raw[vd.Offset:])
	return math.Float64frombits(bits), true
}

// Int returns the int32 value of the named variable.
// Returns (0, false) if the variable does not exist or has a different type.
// For array variables (Count > 1), this returns the first element.
func (s Sample) Int(name string) (int32, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeInt {
		return 0, false
	}
	if vd.Offset+4 > len(s.raw) {
		return 0, false
	}
	return int32(binary.LittleEndian.Uint32(s.raw[vd.Offset:])), true
}

// Bool returns the bool value of the named variable.
// Returns (false, false) if the variable does not exist or has a different type.
// For array variables (Count > 1), this returns the first element.
func (s Sample) Bool(name string) (bool, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeBool {
		return false, false
	}
	if vd.Offset+1 > len(s.raw) {
		return false, false
	}
	return s.raw[vd.Offset] != 0, true
}

// BitField returns the uint32 bit-field value of the named variable.
// Returns (0, false) if the variable does not exist or has a different type.
func (s Sample) BitField(name string) (uint32, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeBitField {
		return 0, false
	}
	if vd.Offset+4 > len(s.raw) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(s.raw[vd.Offset:]), true
}

// Float32s returns all elements of a float32 array variable.
// Returns (nil, false) if the variable does not exist or has a different type.
// For scalar variables (Count == 1), returns a slice of length 1.
func (s Sample) Float32s(name string) ([]float32, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeFloat {
		return nil, false
	}
	if vd.Offset+vd.Count*4 > len(s.raw) {
		return nil, false
	}
	out := make([]float32, vd.Count)
	for i := range out {
		off := vd.Offset + i*4
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(s.raw[off:]))
	}
	return out, true
}

// Float64s returns all elements of a float64 array variable.
// Returns (nil, false) if the variable does not exist or has a different type.
// For scalar variables (Count == 1), returns a slice of length 1.
func (s Sample) Float64s(name string) ([]float64, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeDouble {
		return nil, false
	}
	if vd.Offset+vd.Count*8 > len(s.raw) {
		return nil, false
	}
	out := make([]float64, vd.Count)
	for i := range out {
		off := vd.Offset + i*8
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(s.raw[off:]))
	}
	return out, true
}

// Ints returns all elements of an int32 array variable.
// Returns (nil, false) if the variable does not exist or has a different type.
// For scalar variables (Count == 1), returns a slice of length 1.
func (s Sample) Ints(name string) ([]int32, bool) {
	vd, ok := s.vars[name]
	if !ok || vd.Type != VarTypeInt {
		return nil, false
	}
	if vd.Offset+vd.Count*4 > len(s.raw) {
		return nil, false
	}
	out := make([]int32, vd.Count)
	for i := range out {
		off := vd.Offset + i*4
		out[i] = int32(binary.LittleEndian.Uint32(s.raw[off:]))
	}
	return out, true
}
