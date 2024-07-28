package goreader

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/habak67/gobuffer"
	"github.com/habak67/goerrors"
	"github.com/habak67/gostrings"
	"io"
	"strconv"
	"strings"
)

// Position represents the position in a two-dimensional space containing rows and columns.
type Position struct {
	Row int
	Col int
}

// String returns a string representation of a Position using the format;
//
//	<row>/<column>
func (p Position) String() string {
	return fmt.Sprintf("%d/%d", p.Row, p.Col)
}

// Char represent a rune read by the Reader. A Char contains the read Rune, the Position of the rune in the
// Reader source and an indication if the rune was escaped (\<rune>).
type Char struct {
	Rune    rune
	Pos     Position
	Escaped bool
}

func (c Char) String() string {
	return fmt.Sprintf("<%s%s,[%s]>", gostrings.CondString(c.Escaped, "\\", ""), string(c.Rune), c.Pos)
}

// State holds a state for a Reader. It is used by the methods Reader.State and Reader.Rollback.
type State struct {
	bufState gobuffer.State
}

// New creates a new Reader with a decent buffer size and no transformers. For more configuration of the
// Reader use the function Build.
func New(source io.Reader) *Reader {
	return Builder{}.WithSource(source).Reader()
}

// Builder implements a Reader builder. It is used to create a more customized Reader.
type Builder struct {
	reader *Reader
}

// WithSource adds the source to the Reader to be created.
func (b Builder) WithSource(source io.Reader) Builder {
	return Builder{reader: &Reader{
		reader: bufio.NewReader(source),
		pos:    startPosition,
	}}
}

// WithSize specifies the number of initial rows and the row size for the internal buffer for the Reader to be
// created.
func (b Builder) WithSize(rowSize, rows int) Builder {
	b.reader.buffer = gobuffer.NewWithSize[Char](rowSize, rows)
	return b
}

// WithNormalizeNewline adds a newline normalizer to the Reader to be created. The newline normalizer
// transforms the following rune sequences to a single newline (\u000A).
//
//	CR (\u000D)
//	CR (\u000D) + NL (\u000A)
func (b Builder) WithNormalizeNewline() Builder {
	b.reader.transformers = append(b.reader.transformers, normalizeNewline{})
	return b
}

// WithUnicodeEscape adds a unicode escape transformer to the Reader to be created. A unicode escape transformer
// transform a rune sequence '\uXXXX' to the unicode rune represented by the hexadecimal number 'XXXX'.
func (b Builder) WithUnicodeEscape() Builder {
	b.reader.transformers = append(b.reader.transformers, unicodeEscape{})
	return b
}

// WithRuneEscape adds a rune escape transformer to the Reader to be created. A rune escape transformer
// transform a rune sequence '\<from rune>' to the corresponding <to rune>. The rune escape transformations
// to be used is specified in a map where the <from rune> is the map key and the <to rune> is the map value.
// When reading a rune using Reader.Next we identify an escape sequence that is not configured ('\<rune>' where
// <rune> is not configured in the map) an error is returned from Reader.Next.
//
// When reading runes from the Reader source we read an escaped rune (\<rune>)  Reader.Next will return
// <to rune> if there exist a specified escape <rune> => <to rune>. Otherwise Reader.Next will return <rune>.
// In both cases the Char wrapping the rune will have Char.Escaped set to true.
//
// An example of a rune escape specification:
//
//	map[rune]rune{'t': '\u0009'} will transform a rune sequence "\r" to the tab rune (\u0009).
func (b Builder) WithRuneEscape(escapes map[rune]rune) Builder {
	b.reader.transformers = append(b.reader.transformers, runeEscape{escapes: escapes})
	return b
}

// Reader returns the Reader created from the builder. If no buffer size has been specified using method WithSize
// then a decent default size will be used for the created Reader.
func (b Builder) Reader() *Reader {
	reader := b.reader
	if reader.buffer == nil {
		reader.buffer = gobuffer.NewWithSize[Char](100, 10)
	}
	return reader
}

var startPosition = Position{
	Row: 1,
	Col: 1,
}

// Reader reads runes from an io.Reader specified when creating the Reader. The io.Reader is wrapped in a
// bufio.Reader for better performance and support for reading runes. The read runes are transformed using the
// configured transformers (specified using a reader Builder).
//
// The Reader treats the source as a two-dimensional rune document containing rows and columns. The Reader returns
// the starting position of the read rune. The Reader will bump to a new line when it reads a newline rune (\u000A).
// The Reader may also manage some other common newline sequences using the normalize newline transformer
// (configured with Builder.WithNormalizeNewline). Note that a transformer may convert a sequence of runes into a
// single rune (e.g. parsing escape sequences) read by method Read. The position of each rune will therefore not
// necessarily be consecutive positions.
//
// The Reader supports two models for lookahead.
//
// Reader supports one rune lookahead using the common next/consume pattern. When calling Reader.Next the
// next unread element is returned. Consecutive calls to Reader.Next will return the same element. The current
// next element may be consumed by calling Reader.Consume. The next call to Reader.Next will return the element
// after the consumed element.
//
// Reader also supports multiple element lookahead using rollbacks to a saved state. Reader.State creates a
// state. A call to Reader.Rollback will rollback to the provided state. After a rollback the next element
// returned by Reader.Next will be the "next element" when the state was created.
//
// To mitigate the Reader internal buffer to grow infinitely a Reader may be committed to remove previously read
// elements by calling Reader.Commit. After a commit all runes consumed before the commit will be removed
// (technically consumed runes in the internal buffer row where the read pointer is located will still be
// available in the Reader).
type Reader struct {
	reader       *bufio.Reader
	pos          Position // Position of "next rune"
	buffer       *gobuffer.Buffer[Char]
	transformers []transformer
}

// Next returns the next Char from the Reader. The source Position of the rune is returned. If there are no
// more runes to be read from the configured source an io.EOF error is returned.
//
// If there was an error reading a rune from the source the error is returned. Note that errors (including io.EOF)
// are unrecoverable. If an error is returned by Read the Reader will be put in an error state. All subsequence
// calls to Reader.Next will return a ErrorStateError.
func (r *Reader) Next() (c Char, err error) {
	// If no buffered rune read a new transformed rune from the source and save in the buffer
	if r.buffer.Buffered() == 0 {
		err = r.bufferChar()
		if err != nil {
			return
		}
	}
	var ok bool
	c, ok = r.buffer.Next()
	if !ok {
		// Should really not happen as we have written a char to the buffer above if empty buffer...
		err = fmt.Errorf("unexpected empty buffer")
	}
	return
}

// Consume will consume the next rune (returned by Reader.Next) from the Reader. The next rune (returned by
// Reader.Next) will be the rune after the previous next rune.
func (r *Reader) Consume() {
	r.buffer.Consume()
}

// State returns the current read state for the Reader. The state may be used in a call to Rollback() to
// "reset" the Reader to the current state.
func (r *Reader) State() State {
	return State{bufState: r.buffer.State()}
}

// Rollback resets the Reader to the provided state. After a rollback the next call to method Read will return
// the rune that was the "next rune" when the provided State was created. That is, all runes read since the state
// was created are unread. Note that Rollback() using a state collected before a call to Commit() is not supported
// and may return an error if the rollback state is not valid anymore. Rollback to a zero state (not created by the
// Reader.State method) will return an error.
func (r *Reader) Rollback(state State) error {
	return r.buffer.Rollback(state.bufState)
}

// Commit removes read runes from the internal buffer. It may be used to prevent the Reader from growing indefinitely.
func (r *Reader) Commit() {
	r.buffer.Commit()
}

func (r *Reader) bufferChar() error {
	ru, pos, err := r.readRune()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// We want an unwrapped io.EOF
			return err
		}
		return goerrors.NewPositionalError(pos.Row, pos.Col, fmt.Errorf("error reading rune from source: %w", err))
	}
	// Apply transformers
	c := Char{
		Rune: ru,
		Pos:  pos,
	}
	for _, t := range r.transformers {
		c, err = t.Transform(r, c)
		if err != nil {
			return err
		}
	}
	// Buffer transformed rune
	r.buffer.Write(c)
	return nil
}

func (r *Reader) readRune() (ru rune, pos Position, err error) {
	ru, _, err = r.reader.ReadRune()
	if err != nil {
		pos = r.pos
		return
	}
	pos = r.step(1)
	return
}

func (r *Reader) unreadRune() (err error) {
	err = r.reader.UnreadRune()
	r.step(-1)
	return
}

func (r *Reader) step(i int) (pos Position) {
	pos = r.pos
	r.pos.Col += i
	if r.pos.Col < 0 {
		if r.pos.Row > 0 {
			r.pos.Row -= 1
		}
		r.pos.Col = 0
	}
	return
}

func (r *Reader) newline() {
	r.pos.Row += 1
	r.pos.Col = startPosition.Col
}

type transformer interface {
	Transform(rd *Reader, c Char) (Char, error)
}

// normalizeNewline transform common newline sequences to a single newline (\U000A). The next rune position
// of the provided Reader is bumped to the next row.
type normalizeNewline struct{}

func (n normalizeNewline) Transform(rd *Reader, c Char) (Char, error) {
	switch c.Rune {
	case '\u000A': // NL => NL
		rd.newline()
	case '\u000D': // CR => NL
		c.Rune = '\u000A'
		rd.newline()
		// Check for CR + NL => NL
		r, pos, err := rd.readRune()
		if errors.Is(err, io.EOF) {
			return c, nil
		}
		if err != nil {
			return c, goerrors.NewPositionalError(pos.Row, pos.Col,
				fmt.Errorf("error reading rune from source: %w", err))
		}
		if r == '\u000A' {
			rd.step(-1)
		} else {
			err = rd.unreadRune()
			if err != nil {
				return c, goerrors.NewPositionalError(pos.Row, pos.Col,
					fmt.Errorf("error unreading rune from source: %w", err))
			}
		}
	}
	return c, nil
}

type unicodeEscape struct{}

func (u unicodeEscape) Transform(rd *Reader, c Char) (Char, error) {
	// '\'
	if c.Rune != '\u005C' {
		return c, nil
	}
	r, pos, err := rd.readRune()
	if errors.Is(err, io.EOF) {
		return c, goerrors.NewPositionalError(pos.Row, pos.Col, fmt.Errorf("unexpected EOF reading unicode escape"))
	}
	if err != nil {
		return c, goerrors.NewPositionalError(pos.Row, pos.Col, fmt.Errorf("error reading rune from source: %w", err))
	}
	// 'u'
	if r != 'u' {
		// Not a unicode escape but may be a rune escape.
		err = rd.unreadRune()
		if err != nil {
			return c, goerrors.NewPositionalError(pos.Row, pos.Col,
				fmt.Errorf("error unreading rune from source: %w", err))
		}
		return c, nil
	}
	// Read four hex digits (1234) and create a unicode escape string ('\u1234')
	var sb strings.Builder
	sb.WriteString(`'\u`)
	for i := 1; i <= 4; i++ {
		r, pos, err = rd.readRune()
		if errors.Is(err, io.EOF) {
			return c, goerrors.NewPositionalError(c.Pos.Row, c.Pos.Col, fmt.Errorf("unexpected EOF reading unicode escape"))
		}
		if err != nil {
			return c, goerrors.NewPositionalError(c.Pos.Row, c.Pos.Col, fmt.Errorf("error reading rune from source: %w", err))
		}
		sb.WriteRune(r)
	}
	sb.WriteRune('\'')
	// Transform unicode escape string '\u1234' to the resulting rune
	src := sb.String()
	var res string
	res, err = strconv.Unquote(src)
	if err != nil {
		return c, goerrors.NewPositionalError(c.Pos.Row, c.Pos.Col,
			fmt.Errorf("error parsing unicode escaped rune %s: %w", src, err))
	}
	// As the unquoted string contained a single unicode escape the first rune should be the resulting rune
	c.Rune = []rune(res)[0]
	return c, nil
}

type runeEscape struct {
	escapes map[rune]rune
}

func (e runeEscape) Transform(rd *Reader, c Char) (Char, error) {
	// '\'
	if c.Rune != '\u005C' {
		return c, nil
	}
	// Read escaped rune and transform it if found
	from, _, err := rd.readRune()
	// If EOF we got an illegal incomplete rune escape
	if errors.Is(err, io.EOF) {
		return c, goerrors.NewPositionalError(c.Pos.Row, c.Pos.Col, fmt.Errorf("unexpected EOF reading rune escape"))
	}
	if err != nil {
		return c, goerrors.NewPositionalError(c.Pos.Row, c.Pos.Col, fmt.Errorf("error reading rune from source: %w", err))
	}
	to, ok := e.escapes[from]
	if ok {
		c.Rune = to
	} else {
		c.Rune = from
	}
	c.Escaped = true
	return c, nil
}
