package goreader

import (
	"errors"
	"fmt"
	"github.com/habak67/gobuffer"
	"github.com/habak67/goerrors"
	"io"
	"strings"
	"testing"
)

func TestCharReaderRollback_ZeroState(t *testing.T) {
	reader := New(strings.NewReader("ab"))
	c, _ := reader.Next()
	if c.Rune != 'a' {
		t.Errorf("expected next to be 'a' (got %c)", c.Rune)
	}
	err := reader.Rollback(State{})
	if err == nil || err.Error() != gobuffer.ZeroStateError.Error() {
		t.Errorf("expcted error rollback using zero state")
	}
}

func TestCharReaderRollback_IllegalState(t *testing.T) {
	reader := Builder{}.WithSource(strings.NewReader("12345678901234567890")).WithSize(10, 5).Reader()
	state := reader.State()
	for i := 0; i < 15; i++ {
		_, err := reader.Next()
		if err != nil {
			t.Errorf("unexpected error from next: %v", err)
			return
		}
		reader.Consume()
	}
	reader.Commit()
	err := reader.Rollback(state)
	if err == nil || err.Error() != gobuffer.IllegalStateError.Error() {
		t.Errorf("expected error rollback to illegal state (got %v)", err)
	}
}

func TestBuilder_NoSourcePanic(t *testing.T) {
	defer func() { recover() }()
	_ = Builder{}.WithSize(10, 5).Reader()
	t.Errorf("Builder.Reader should have raised a panic.")
}

func TestReader(t *testing.T) {
	tests := []struct {
		name   string
		reader *Reader
		ops    []any
	}{
		{
			name:   "multiple next and single consume",
			reader: Builder{}.WithSource(strings.NewReader("a")).Reader(),
			ops: []any{
				opNext[Char]{newChar('a', 1, 1)},
				opNext[Char]{newChar('a', 1, 1)},
				opNext[Char]{newChar('a', 1, 1)},
				opConsume{},
				opEOF{},
			},
		},
		{
			name:   "multiple EOF at end",
			reader: Builder{}.WithSource(strings.NewReader("a")).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opEOF{},
				opEOF{},
				opEOF{},
			},
		},
		{
			name:   "multiple next and consume",
			reader: Builder{}.WithSource(strings.NewReader("abc")).Reader(),
			ops: []any{
				opNext[Char]{newChar('a', 1, 1)},
				opNext[Char]{newChar('a', 1, 1)},
				opConsume{},
				opNext[Char]{newChar('b', 1, 2)},
				opConsume{},
				opNext[Char]{newChar('c', 1, 3)},
				opNext[Char]{newChar('c', 1, 3)},
				opConsume{},
				opEOF{},
			},
		},
		{
			name:   "pos",
			reader: Builder{}.WithSource(strings.NewReader("ab\nc")).WithNormalizeNewline().Reader(),
			ops: []any{
				opPos{Pos: Position{Row: 1, Col: 1}},
				opNext[Char]{newChar('a', 1, 1)},
				opPos{Pos: Position{Row: 1, Col: 1}},
				opConsume{},
				opPos{Pos: Position{Row: 1, Col: 2}},
				opNext[Char]{newChar('b', 1, 2)},
				opConsume{},
				opPos{Pos: Position{Row: 1, Col: 3}},
				opNext[Char]{newChar('\n', 1, 3)},
				opConsume{},
				opPos{Pos: Position{Row: 2, Col: 1}},
				opNext[Char]{newChar('c', 2, 1)},
				opPos{Pos: Position{Row: 2, Col: 1}},
				opConsume{},
				opEOF{},
			},
		},
		{
			name:   "repeating EOF",
			reader: Builder{}.WithSource(strings.NewReader("a")).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opEOF{},
				opEOF{},
				opEOF{},
			},
		},
		{
			name:   "error from internal reader",
			reader: Builder{}.WithSource(&errorReader{Input: "ab"}).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextErr[Char]{Err: genError(1, 3, fmt.Errorf("error reading rune from source: %w", errorReaderError))},
				opNextErr[Char]{Err: genError(1, 3, fmt.Errorf("error reading rune from source: %w", errorReaderError))},
				opNextErr[Char]{Err: genError(1, 3, fmt.Errorf("error reading rune from source: %w", errorReaderError))},
			},
		},
		{
			name:   "state and rollback",
			reader: Builder{}.WithSource(strings.NewReader("abcd")).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opState{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextAndConsume[Char]{newChar('c', 1, 3)},
				opRollback{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextAndConsume[Char]{newChar('c', 1, 3)},
				opNextAndConsume[Char]{newChar('d', 1, 4)},
				opEOF{},
			},
		},
		{
			name:   "state and rollback after EOF",
			reader: Builder{}.WithSource(strings.NewReader("abc")).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opState{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextAndConsume[Char]{newChar('c', 1, 3)},
				opEOF{},
				opRollback{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextAndConsume[Char]{newChar('c', 1, 3)},
				opEOF{},
			},
		},
		{
			name:   "state and rollback after internal reader error",
			reader: Builder{}.WithSource(&errorReader{Input: "ab"}).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opState{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextErr[Char]{Err: genError(1, 3, fmt.Errorf("error reading rune from source: %w", errorReaderError))},
				opRollback{},
				opNextAndConsume[Char]{newChar('b', 1, 2)},
				opNextErr[Char]{Err: genError(1, 3, fmt.Errorf("error reading rune from source: %w", errorReaderError))},
			},
		},
		{
			name:   "transformer NormalizeNewline",
			reader: Builder{}.WithSource(strings.NewReader("a\u000Ab\u000Dc\u000D\u000Ad")).WithNormalizeNewline().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('\n', 1, 2)},
				opNextAndConsume[Char]{newChar('b', 2, 1)},
				opNextAndConsume[Char]{newChar('\n', 2, 2)},
				opNextAndConsume[Char]{newChar('c', 3, 1)},
				opNextAndConsume[Char]{newChar('\n', 3, 2)},
				opNextAndConsume[Char]{newChar('d', 4, 1)},
				opEOF{},
			},
		},
		{
			name:   "transformer NormalizeNewline EOF after NL",
			reader: Builder{}.WithSource(strings.NewReader("a\u000Ab\u000A")).WithNormalizeNewline().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('\n', 1, 2)},
				opNextAndConsume[Char]{newChar('b', 2, 1)},
				opNextAndConsume[Char]{newChar('\n', 2, 2)},
				opEOF{},
			},
		},
		{
			name:   "transformer NormalizeNewline EOF after CR",
			reader: Builder{}.WithSource(strings.NewReader("a\u000Ab\u000D")).WithNormalizeNewline().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('\n', 1, 2)},
				opNextAndConsume[Char]{newChar('b', 2, 1)},
				opNextAndConsume[Char]{newChar('\n', 2, 2)},
				opEOF{},
			},
		},
		{
			name:   "transformer NormalizeNewline EOF after CR + NL",
			reader: Builder{}.WithSource(strings.NewReader("a\u000Ab\u000D\u000A")).WithNormalizeNewline().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('\n', 1, 2)},
				opNextAndConsume[Char]{newChar('b', 2, 1)},
				opNextAndConsume[Char]{newChar('\n', 2, 2)},
				opEOF{},
			},
		},
		{
			name:   "transformer UnicodeEscape",
			reader: Builder{}.WithSource(strings.NewReader(`a\u0058`)).WithUnicodeEscape().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('X', 1, 2)},
				opEOF{},
			},
		},
		{
			name:   "transformer UnicodeEscape invalid hex number",
			reader: Builder{}.WithSource(strings.NewReader(`a\u005X`)).WithUnicodeEscape().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextErr[Char]{Err: genError(1, 2, errors.New(`error parsing unicode escaped rune '\u005X': invalid syntax`))},
				opEOF{},
			},
		},
		{
			name:   "transformer UnicodeEscape invalid hex number space",
			reader: Builder{}.WithSource(strings.NewReader(`a\u005 `)).WithUnicodeEscape().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextErr[Char]{Err: genError(1, 2, errors.New(`error parsing unicode escaped rune '\u005 ': invalid syntax`))},
				opEOF{},
			},
		},
		{
			name:   "transformer UnicodeEscape unexpected EOF",
			reader: Builder{}.WithSource(strings.NewReader(`a\u005`)).WithUnicodeEscape().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextErr[Char]{Err: genError(1, 2, errors.New("unexpected EOF reading unicode escape"))},
				opEOF{},
			},
		},
		{
			name:   "transformer UnicodeEscape rune escape",
			reader: Builder{}.WithSource(strings.NewReader(`a\X`)).WithUnicodeEscape().Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newChar('\\', 1, 2)},
				opNextAndConsume[Char]{newChar('X', 1, 3)},
				opEOF{},
			},
		},
		{
			name: "transformer RuneEscape",
			reader: Builder{}.WithSource(strings.NewReader(`a\a\b \c\X\\`)).WithRuneEscape(map[rune]rune{
				'a': 'x',
				'b': 'y',
				'c': 'z',
			}).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextAndConsume[Char]{newCharEscaped('x', 1, 2)},
				opNextAndConsume[Char]{newCharEscaped('y', 1, 4)},
				opNextAndConsume[Char]{newChar(' ', 1, 6)},
				opNextAndConsume[Char]{newCharEscaped('z', 1, 7)},
				opNextAndConsume[Char]{newCharEscaped('X', 1, 9)},
				opNextAndConsume[Char]{newCharEscaped('\\', 1, 11)},
				opEOF{},
			},
		},
		{
			name: "transformer RuneEscape unexpected EOF",
			reader: Builder{}.WithSource(strings.NewReader(`a\`)).WithRuneEscape(map[rune]rune{
				'a': 'x',
				'b': 'y',
				'c': 'z',
			}).Reader(),
			ops: []any{
				opNextAndConsume[Char]{newChar('a', 1, 1)},
				opNextErr[Char]{Err: genError(1, 2, errors.New("unexpected EOF reading rune escape"))},
				opEOF{},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var state State
			reader := test.reader
			for i, o := range test.ops {
				switch op := o.(type) {
				case opNext[Char]:
					c, err := reader.Next()
					if err != nil {
						t.Errorf("[%d] unexpected next error: %s", i, err)
					}
					if c != op.Exp {
						t.Errorf("[%d] unexpected char from next:\nexp=%v\ngot=%v", i, op.Exp, c)
					}
				case opNextAndConsume[Char]:
					c, err := reader.Next()
					if err != nil {
						t.Errorf("[%d] unexpected next error: %s", i, err)
					}
					if c != op.Exp {
						t.Errorf("[%d] unexpected char from next:\nexp=%v\ngot=%v", i, op.Exp, c)
					}
					reader.Consume()
				case opNextErr[Char]:
					_, err := reader.Next()
					if err == nil || err.Error() != op.Err.Error() {
						t.Errorf("[%d] unexpected next error:\nexp=%v\ngot=%v", i, op.Err, err)
					}
				case opConsume:
					reader.Consume()
				case opState:
					state = reader.State()
				case opRollback:
					err := reader.Rollback(state)
					if !errors.Is(err, op.Err) {
						t.Errorf("[%d] unexpected error:\nexp=%v\ngot=%v", i, op.Err, err)
					}
				case opCommit:
					reader.Commit()
				case opEOF:
					c, err := reader.Next()
					if !errors.Is(err, io.EOF) {
						t.Errorf("[%d] expected EOF (got char: %s, error: %v)", i, c, err)
					}
				}
			}
		})
	}
}

func newChar(r rune, row, col int) Char {
	return Char{Rune: r, Pos: Position{Row: row, Col: col}}
}

func newCharEscaped(r rune, row, col int) Char {
	return Char{Rune: r, Pos: Position{Row: row, Col: col}, Escaped: true}
}

type opNext[T any] struct {
	Exp T
}

type opNextAndConsume[T any] struct {
	Exp T
}

type opNextErr[T any] struct {
	Err error
}

type opConsume struct{}

type opState struct{}

type opRollback struct {
	Err error
}

type opCommit struct{}

type opEOF struct{}

type opPos struct {
	Pos Position
}

var errorReaderError = errors.New("reader test error")

type errorReader struct {
	Input string
	idx   int
}

func genError(row, col int, err error) error {
	return goerrors.NewPositionalError(row, col, err)
}

func (e *errorReader) Read(p []byte) (n int, err error) {
	if e.idx >= len(e.Input) {
		return 0, errorReaderError
	}
	m := min(len(p), len(e.Input)-e.idx)
	for n = 0; n < m; n++ {
		p[n] = e.Input[n+e.idx]
	}
	e.idx += m
	return
}
