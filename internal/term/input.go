package term

import (
	"strings"
)

// KeyState is the terminal mode information needed to encode a key press.
type KeyState struct {
	AppCursor      bool
	AppKeypad      bool
	AltScreen      bool
	BracketedPaste bool
}

// EncodeKey converts a Gio key name plus modifiers into the byte sequence a
// terminal application expects. ok is false when the key should be ignored.
func EncodeKey(name string, mods Mods, st KeyState) (out string, ok bool) {
	alt := mods&ModAlt != 0
	ctrl := mods&ModCtrl != 0
	shift := mods&ModShift != 0

	// Modifier parameter for CSI sequences: 1 + (shift, alt, ctrl) bits.
	mod := 1
	if shift {
		mod += 1
	}
	if alt {
		mod += 2
	}
	if ctrl {
		mod += 4
	}

	withMod := func(seq string) string {
		if mod == 1 || len(seq) < 2 {
			return seq
		}
		body := seq[:len(seq)-1]
		final := seq[len(seq)-1:]
		if body == "\x1b[" || body == "\x1bO" {
			// Supply the omitted first parameter: CSI D -> CSI 1;5D.
			return "\x1b[1;" + itoa(mod) + final
		}
		return body + ";" + itoa(mod) + final
	}

	// cursorSeq uses the SS3 form only for unmodified keys in application
	// cursor mode; modified keys always use the CSI form.
	cursorSeq := func(csi, ss3 string) string {
		if st.AppCursor && mod == 1 {
			return ss3
		}
		return withMod(csi)
	}

	switch name {
	case "⏎", "⌤":
		if alt {
			return "\x1b\r", true
		}
		return "\r", true
	case "Tab":
		if shift {
			return "\x1b[Z", true
		}
		return "\t", true
	case "⎋":
		return "\x1b", true
	case "⌫":
		if ctrl {
			return "\x08", true
		}
		if alt {
			return "\x1b\x7f", true
		}
		return "\x7f", true
	case "⌦":
		return withMod("\x1b[3~"), true
	case "⇱":
		return cursorSeq("\x1b[H", "\x1bOH"), true
	case "⇲":
		return cursorSeq("\x1b[F", "\x1bOF"), true
	case "←":
		return cursorSeq("\x1b[D", "\x1bOD"), true
	case "→":
		return cursorSeq("\x1b[C", "\x1bOC"), true
	case "↑":
		return cursorSeq("\x1b[A", "\x1bOA"), true
	case "↓":
		return cursorSeq("\x1b[B", "\x1bOB"), true
	case "⇞":
		return withMod("\x1b[5~"), true
	case "⇟":
		return withMod("\x1b[6~"), true
	case "Insert":
		return withMod("\x1b[2~"), true
	case "Space":
		if ctrl {
			return "\x00", true
		}
		if alt {
			return "\x1b ", true
		}
		return " ", true
	}

	// Function keys.
	if strings.HasPrefix(name, "F") && len(name) <= 3 {
		if seq, ok := funcKey(name, mod); ok {
			return seq, true
		}
	}

	if len(name) != 1 {
		return "", false
	}
	r := rune(name[0])

	// Ctrl+letter produces a control character.
	if ctrl {
		if b, ok := ctrlByte(r); ok {
			if alt {
				return "\x1b" + string([]byte{b}), true
			}
			return string([]byte{b}), true
		}
	}
	if alt {
		return "\x1b" + string(r), true
	}
	return string(r), true
}

// Mods mirrors key.Modifiers without importing Gio here.
type Mods uint32

const (
	ModCtrl Mods = 1 << iota
	ModShift
	ModAlt
	ModSuper
)

func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r-'a') + 1, true
	case r >= 'A' && r <= 'Z':
		return byte(r-'A') + 1, true
	}
	switch r {
	case '@', ' ':
		return 0, true
	case '[':
		return 0x1b, true
	case '\\':
		return 0x1c, true
	case ']':
		return 0x1d, true
	case '^':
		return 0x1e, true
	case '_':
		return 0x1f, true
	case '?':
		return 0x7f, true
	}
	return 0, false
}

func funcKey(name string, mod int) (string, bool) {
	var n int
	switch name {
	case "F1":
		n = 1
	case "F2":
		n = 2
	case "F3":
		n = 3
	case "F4":
		n = 4
	case "F5":
		n = 5
	case "F6":
		n = 6
	case "F7":
		n = 7
	case "F8":
		n = 8
	case "F9":
		n = 9
	case "F10":
		n = 10
	case "F11":
		n = 11
	case "F12":
		n = 12
	default:
		return "", false
	}
	if n <= 4 {
		final := string("PQRS"[n-1])
		if mod == 1 {
			return "\x1bO" + final, true
		}
		return "\x1b[1;" + itoa(mod) + final, true
	}
	var code int
	switch n {
	case 5:
		code = 15
	case 6:
		code = 17
	case 7:
		code = 18
	case 8:
		code = 19
	case 9:
		code = 20
	case 10:
		code = 21
	case 11:
		code = 23
	case 12:
		code = 24
	}
	if mod == 1 {
		return "\x1b[" + itoa(code) + "~", true
	}
	return "\x1b[" + itoa(code) + ";" + itoa(mod) + "~", true
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

const (
	MousePressOnly = 9
	MouseClicks    = 1000
	MouseDrag      = 1002
	MouseMotion    = 1003
)

type MouseEncoding uint8

const (
	MouseX10 MouseEncoding = iota
	MouseUTF8
	MouseSGR
	MouseURXVT
	MousePixels
)

// EncodeMouse encodes a report in the format selected by the application.
func EncodeMouse(button, x, y int, press bool, mods Mods, encoding MouseEncoding) string {
	if x < 0 || y < 0 {
		return ""
	}
	flags := 0
	if mods&ModShift != 0 {
		flags |= 4
	}
	if mods&ModAlt != 0 {
		flags |= 8
	}
	if mods&ModCtrl != 0 {
		flags |= 16
	}
	if encoding == MouseSGR || encoding == MousePixels {
		final := 'M'
		if !press {
			final = 'm'
		}
		return "\x1b[<" + itoa(button+flags) + ";" + itoa(x+1) + ";" + itoa(y+1) + string(final)
	}
	if !press {
		button = 3
	}
	code := button + flags + 32
	if encoding == MouseURXVT {
		return "\x1b[" + itoa(code) + ";" + itoa(x+1) + ";" + itoa(y+1) + "M"
	}
	if encoding == MouseUTF8 {
		if x > 2014 || y > 2014 {
			return ""
		}
		return "\x1b[M" + string([]rune{rune(code), rune(x + 33), rune(y + 33)})
	}
	if x > 222 || y > 222 {
		return ""
	}
	return "\x1b[M" + string([]byte{byte(code), byte(x + 33), byte(y + 33)})
}

// Paste prepares pasted text for the shell, honouring bracketed paste mode.
func Paste(text string, bracketed bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if bracketed {
		return "\x1b[200~" + text + "\x1b[201~"
	}
	return text
}
