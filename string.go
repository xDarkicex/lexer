package lexer

// DecodeStringLiteralInto returns the bytes represented by a SQL single-quoted
// literal whose span includes the surrounding quotes. The common case (no
// escape sequence) returns a subslice of src and allocates nothing. Escaped
// forms are written into scratch; callers own its capacity and can keep one
// reusable buffer per execution context. This helper never allocates. It
// returns ok=false when scratch is too small.
//
// Unknown backslash sequences are preserved byte-for-byte.  This keeps paths
// such as `C:\\tmp` lossless while accepting the compatibility form `\\'`.
func DecodeStringLiteralInto(src []byte, start, end uint32, scratch []byte) ([]byte, bool) {
	if start >= end || int(end) > len(src) || end-start < 2 {
		return nil, false
	}
	inner := src[start+1 : end-1]
	needsDecode := false
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) && (inner[i+1] == '\\' || inner[i+1] == '\'') {
			needsDecode = true
			break
		}
		if inner[i] == '\'' && i+1 < len(inner) && inner[i+1] == '\'' {
			needsDecode = true
			break
		}
	}
	if !needsDecode {
		return inner, true
	}

	decodedLen := len(inner)
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) && (inner[i+1] == '\\' || inner[i+1] == '\'') {
			decodedLen--
			i++
			continue
		}
		if inner[i] == '\'' && i+1 < len(inner) && inner[i+1] == '\'' {
			decodedLen--
			i++
		}
	}
	if cap(scratch) < decodedLen {
		return nil, false
	}
	out := scratch[:decodedLen]
	write := 0
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) && (inner[i+1] == '\\' || inner[i+1] == '\'') {
			out[write] = inner[i+1]
			write++
			i++
			continue
		}
		if inner[i] == '\'' && i+1 < len(inner) && inner[i+1] == '\'' {
			out[write] = '\''
			write++
			i++
			continue
		}
		out[write] = inner[i]
		write++
	}
	return out, true
}

// DecodeEscapeStringLiteralInto applies PostgreSQL E'...' escapes without
// allocating. It supports the common single-byte escapes and SQL doubled
// quotes; unknown backslash sequences remain lossless.
func DecodeEscapeStringLiteralInto(src []byte, start, end uint32, scratch []byte) ([]byte, bool) {
	if start >= end || int(end) > len(src) || end-start < 2 {
		return nil, false
	}
	inner := src[start+1 : end-1]
	decodedLen := len(inner)
	for i := 0; i+1 < len(inner); i++ {
		if inner[i] == '\\' && isEscapeByte(inner[i+1]) {
			decodedLen--
			i++
			continue
		}
		if inner[i] == '\'' && inner[i+1] == '\'' {
			decodedLen--
			i++
		}
	}
	if cap(scratch) < decodedLen {
		return nil, false
	}
	out := scratch[:decodedLen]
	write := 0
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) && isEscapeByte(inner[i+1]) {
			switch inner[i+1] {
			case 'n':
				out[write] = '\n'
			case 't':
				out[write] = '\t'
			case 'r':
				out[write] = '\r'
			case 'b':
				out[write] = '\b'
			case 'f':
				out[write] = '\f'
			case '0':
				out[write] = 0
			default:
				out[write] = inner[i+1]
			}
			write++
			i++
			continue
		}
		if inner[i] == '\'' && i+1 < len(inner) && inner[i+1] == '\'' {
			out[write] = '\''
			write++
			i++
			continue
		}
		out[write] = inner[i]
		write++
	}
	return out, true
}

func isEscapeByte(b byte) bool {
	switch b {
	case 'n', 't', 'r', 'b', 'f', '0', '\\', '\'', '"':
		return true
	default:
		return false
	}
}
