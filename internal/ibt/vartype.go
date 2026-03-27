package ibt

// VarType is the iRacing telemetry variable type discriminant.
type VarType int32

const (
	VarTypeChar     VarType = 0
	VarTypeBool     VarType = 1
	VarTypeInt      VarType = 2
	VarTypeBitField VarType = 3
	VarTypeFloat    VarType = 4
	VarTypeDouble   VarType = 5
)

// String returns a human-readable name for the type.
func (t VarType) String() string {
	switch t {
	case VarTypeChar:
		return "char"
	case VarTypeBool:
		return "bool"
	case VarTypeInt:
		return "int32"
	case VarTypeBitField:
		return "bitField"
	case VarTypeFloat:
		return "float32"
	case VarTypeDouble:
		return "float64"
	default:
		return "unknown"
	}
}

// elemSize returns the byte size of one element of this type.
// Returns 0 for unknown types.
func (t VarType) elemSize() int {
	switch t {
	case VarTypeChar, VarTypeBool:
		return 1
	case VarTypeInt, VarTypeBitField, VarTypeFloat:
		return 4
	case VarTypeDouble:
		return 8
	default:
		return 0
	}
}
