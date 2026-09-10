package delimited

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// The byte order marks a file may open with to say outright what it is written
// in. None of them is valid UTF-8 text on its own, so a file that starts with
// one is telling us its encoding rather than holding those bytes by accident.
var (
	utf8BOM    = []byte{0xEF, 0xBB, 0xBF}
	utf16LEBOM = []byte{0xFF, 0xFE}
	utf16BEBOM = []byte{0xFE, 0xFF}
	utf32LEBOM = []byte{0xFF, 0xFE, 0x00, 0x00}
)

// ToUTF8 reads a body as the encoding it announces and hands back UTF-8 text
// with the announcement removed. A file that announces nothing is handed back
// untouched, for the caller to accept or refuse — without a mark there is no
// telling UTF-16 from a file that happens to hold those bytes, and this rule
// guesses at nothing.
//
// Windows tools write UTF-16 far more often than anyone expects: Windows
// PowerShell's Export-Csv and Out-File default to UTF-16LE with a mark, and
// Excel's "유니코드 텍스트" save does the same. Such a file is not broken and
// not ambiguous — it says on its first two bytes what it is — but read as
// UTF-8 it is not text at all, so the importer used to refuse the whole
// upload with "CSV must be UTF-8 encoded" and leave no way out.
func ToUTF8(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, utf8BOM):
		return data[len(utf8BOM):]
	case bytes.HasPrefix(data, utf32LEBOM):
		// UTF-32LE opens with the UTF-16LE mark and two zero bytes. Reading it
		// as UTF-16 would make garbage out of a file we simply cannot read, so
		// leave it alone and let the caller refuse it.
		return data
	case bytes.HasPrefix(data, utf16LEBOM):
		return fromUTF16(data[len(utf16LEBOM):], binary.LittleEndian)
	case bytes.HasPrefix(data, utf16BEBOM):
		return fromUTF16(data[len(utf16BEBOM):], binary.BigEndian)
	}
	return data
}

// fromUTF16 turns pairs of bytes into UTF-8 text. A lone byte at the end is a
// half-written character and is dropped; a lone surrogate becomes U+FFFD, the
// character that stands for "unreadable here", so one bad pair costs its own
// character and not the file.
func fromUTF16(data []byte, order binary.ByteOrder) []byte {
	units := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		units = append(units, order.Uint16(data[index:index+2]))
	}
	return []byte(string(utf16.Decode(units)))
}
