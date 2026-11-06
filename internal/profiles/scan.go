package profiles

import "bytes"

// fieldScanner picks a response's own fields out of JSON as it goes by, one byte
// at a time, in any size of chunk. It keeps only the values it wants, and skips
// everything else without holding it: a Responses API stream's last event holds
// the whole response, and an embeddings response can run to many megabytes.
//
// It knows only the shapes it reads, with the depth of each. A response's fields
// are at the top of the document, or one level down in the "response" or "message"
// object of a stream's event. So what a model writes (in a string), or what an
// API nests elsewhere, is never taken for them.
//
// JSON it doesn't expect can only cost it the fields: nothing here fails.
type fieldScanner struct {
	state scanState

	stack [4]frame // the frames are at most document, record, output, item
	depth int

	key    [16]byte // long enough for every key wanted
	keyLen int      // over len(key) when it can't be one of them
	keyEsc bool     // the last byte of the key was a backslash

	want wanted // what the value being read is for

	// The value being read.
	nest     int
	inString bool
	escaped  bool
	capture  []byte // when want is a field
	tooLong  bool
}

// maxField is the most of one wanted value kman holds. A usage object is small.
const maxField = 64 << 10

type scanState uint8

const (
	scanDocument  scanState = iota // waiting for a document to begin
	scanIgnore                     // not a document it can read: nothing more until a reset
	scanKeyOrEnd                   // in an object
	scanKey                        // in a key
	scanColon                      // after a key
	scanValueHead                  // after the colon
	scanValue                      // in a value it isn't looking inside
	scanItem                       // in the output array
)

type frame uint8

const (
	frameDocument frame = iota // the top object: a record, which may hold another
	frameRecord                // the object under "response" or "message"
	frameOutput                // the array under "output"
	frameItem                  // an object in it
)

type wanted uint8

const (
	wantNothing wanted = iota
	wantModel
	wantTier
	wantUsage
	wantItemType
)

// reset readies it for the next document.
func (s *fieldScanner) reset() {
	s.state, s.depth = scanDocument, 0
}

func (s *fieldScanner) feed(p []byte, got *reported) {
	for i := 0; i < len(p); {
		b := p[i]
		switch s.state {
		case scanIgnore:
			return

		case scanDocument:
			switch {
			case isSpace(b):
			case b == '{':
				s.push(frameDocument)
			default:
				s.state = scanIgnore
			}
			i++

		case scanKeyOrEnd:
			switch {
			case isSpace(b) || b == ',':
			case b == '"':
				s.state, s.keyLen, s.keyEsc = scanKey, 0, false
			case b == '}':
				s.pop()
			default:
				s.state = scanIgnore
			}
			i++

		case scanKey:
			i++
			switch {
			case s.keyEsc:
				s.keyEsc = false
			case b == '\\':
				// A key written with an escape isn't one kman wants: no provider does it.
				s.keyEsc, s.keyLen = true, len(s.key)+1
			case b == '"':
				s.state = scanColon
			case s.keyLen < len(s.key):
				s.key[s.keyLen] = b
				s.keyLen++
			default:
				s.keyLen = len(s.key) + 1
			}

		case scanColon:
			switch {
			case isSpace(b):
			case b == ':':
				s.state = scanValueHead
			default:
				s.state = scanIgnore
			}
			i++

		case scanValueHead:
			if isSpace(b) {
				i++
				continue
			}
			if s.enter(b, got) {
				i++
				continue
			}
			s.beginValue()

		case scanItem:
			switch {
			case isSpace(b) || b == ',':
				i++
			case b == ']':
				s.pop()
				i++
			case b == '{':
				s.push(frameItem)
				i++
			default:
				s.want = wantNothing
				s.beginValue()
			}

		case scanValue:
			i = s.value(p, i, got)
		}
	}
}

// enter goes into the value of the key just read, if it's one kman looks inside,
// and otherwise says what's wanted from it.
func (s *fieldScanner) enter(b byte, got *reported) bool {
	s.want = wantNothing
	if s.keyLen > len(s.key) {
		return false
	}
	key := string(s.key[:s.keyLen]) // the compiler allocates nothing to switch on this
	switch s.stack[s.depth-1] {
	case frameItem:
		if key == "type" {
			s.want = wantItemType
		}
		return false
	case frameDocument:
		if (key == "response" || key == "message") && b == '{' {
			s.push(frameRecord)
			return true
		}
	}
	switch key {
	case "model":
		s.want = wantModel
	case "service_tier":
		s.want = wantTier
	case "usage":
		s.want = wantUsage
	case "output":
		if b == '[' {
			got.tools = [len(toolNames)]int64{} // the last list is the whole list
			s.push(frameOutput)
			return true
		}
	}
	return false
}

func (s *fieldScanner) push(f frame) {
	s.stack[s.depth] = f
	s.depth++
	s.resume()
}

func (s *fieldScanner) pop() {
	s.depth--
	s.resume()
}

// resume continues in the frame on top of the stack.
func (s *fieldScanner) resume() {
	switch {
	case s.depth == 0:
		s.state = scanDocument
	case s.stack[s.depth-1] == frameOutput:
		s.state = scanItem
	default:
		s.state = scanKeyOrEnd
	}
}

func (s *fieldScanner) beginValue() {
	s.state, s.nest, s.inString, s.escaped, s.tooLong = scanValue, 0, false, false, false
	s.capture = s.capture[:0]
}

// value reads a value from p[i:], up to its end, and returns where it stopped.
func (s *fieldScanner) value(p []byte, i int, got *reported) int {
	for ; i < len(p); i++ {
		b := p[i]
		if s.inString {
			if s.want == wantNothing {
				// The fast path: most of a response is a string nobody wants.
				for !s.escaped && i < len(p) && p[i] != '"' && p[i] != '\\' {
					i++
				}
				if i == len(p) {
					return i
				}
				b = p[i]
			} else {
				s.keep(b)
			}
			switch {
			case s.escaped:
				s.escaped = false
			case b == '\\':
				s.escaped = true
			case b == '"':
				s.inString = false
				if s.nest == 0 {
					s.finish(got)
					return i + 1
				}
			}
			continue
		}
		switch b {
		case '"':
			s.inString = true
		case '{', '[':
			s.nest++
		case '}', ']':
			if s.nest == 0 { // the end of a number or a word, and of what holds it
				s.finish(got)
				return i
			}
			s.nest--
			if s.nest == 0 {
				s.keep(b)
				s.finish(got)
				return i + 1
			}
		case ',', ' ', '\t', '\r', '\n':
			if s.nest == 0 {
				s.finish(got)
				return i
			}
		}
		s.keep(b)
	}
	return i
}

func (s *fieldScanner) keep(b byte) {
	if s.want == wantNothing || s.tooLong {
		return
	}
	if len(s.capture) == maxField {
		s.tooLong = true
		return
	}
	s.capture = append(s.capture, b)
}

// finish hands over a value, whole, and continues after it.
func (s *fieldScanner) finish(got *reported) {
	s.resume()
	if s.want == wantNothing || s.tooLong {
		return
	}
	switch s.want {
	case wantModel:
		got.model.set(s.capture)
	case wantTier:
		got.tier.set(s.capture)
	case wantUsage:
		got.setUsage(s.capture)
	case wantItemType:
		got.setTool(s.capture)
	}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// sseLines finds the JSON in a server-sent event stream, by the format's rules:
// lines end in LF, CR or CRLF, a blank line ends an event, and an event's data
// lines are one value. It passes a data line on as it arrives, rather than
// holding it until its end, because one event can hold a whole response.
type sseLines struct {
	state    sseState
	fieldLen int  // how much of the field name has matched "data"
	afterCR  bool // the last line ended in CR, so an LF now is part of that
}

type sseState uint8

const (
	sseLineStart sseState = iota
	sseField
	sseDataHead // just after "data:", where one space is ignored
	sseData
	sseSkipLine
)

const sseDataField = "data"

var sseNewline = []byte{'\n'}

func (l *sseLines) feed(p []byte, s *fieldScanner, got *reported) {
	for i := 0; i < len(p); {
		b := p[i]
		switch l.state {
		case sseLineStart:
			if l.afterCR {
				l.afterCR = false
				if b == '\n' {
					i++
					continue
				}
			}
			if b == '\n' || b == '\r' { // a blank line ends the event
				s.reset()
				l.afterCR = b == '\r'
				i++
				continue
			}
			l.state, l.fieldLen = sseField, 0

		case sseField:
			i++
			switch {
			case b == '\n' || b == '\r':
				l.endLine(b) // a field with no value: nothing wanted has none
			case b == ':' && l.fieldLen == len(sseDataField):
				l.state = sseDataHead
			case b == ':':
				l.state = sseSkipLine
			case l.fieldLen < len(sseDataField) && b == sseDataField[l.fieldLen]:
				l.fieldLen++
			default:
				l.state = sseSkipLine
			}

		case sseDataHead:
			if b == ' ' {
				i++
			}
			l.state = sseData

		case sseData, sseSkipLine:
			end := bytes.IndexAny(p[i:], "\r\n")
			line := p[i:]
			if end >= 0 {
				line = p[i : i+end]
			}
			if l.state == sseData {
				s.feed(line, got)
			}
			i += len(line)
			if end >= 0 {
				if l.state == sseData {
					s.feed(sseNewline, got) // how data lines are joined
				}
				l.endLine(p[i])
				i++
			}
		}
	}
}

func (l *sseLines) endLine(b byte) {
	l.state, l.afterCR = sseLineStart, b == '\r'
}
