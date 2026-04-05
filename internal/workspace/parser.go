package workspace

import (
	"fmt"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

var (
	dojoLexer = lexer.MustSimple([]lexer.SimpleRule{
		{Name: "Arrow", Pattern: `->`},
		{Name: "Colon", Pattern: `:`},
		{Name: "String", Pattern: `"(?:[^"\\]|\\.)*"`},
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_./-]*`},
		{Name: "Whitespace", Pattern: `[ \t]+`},
		{Name: "Newline", Pattern: `[\n\r]+`},
	})

	dojoParser = participle.MustBuild[Document](
		participle.Lexer(dojoLexer),
		participle.Elide("Whitespace"),
	)
)

// Document represents a complete parsed test.plan script.
type Document struct {
	Lines []*Line `parser:"(Newline* @@ Newline*)*"`
}

// Line represents a single action/expectation in the DSL. Clauses are optional;
// a bare Expect -> name registers the expectation without constraints.
type Line struct {
	Action  string    `parser:"@Ident Arrow"`
	Target  string    `parser:"@Ident"`
	Clauses []*Clause `parser:"(Arrow @@ (Arrow @@)*)?"`
}

// Clause represents key-value pairs (e.g., Payload: file.json).
type Clause struct {
	KeyParts []string `parser:"@Ident+"`
	RawValue *string  `parser:"(Colon (@String | @Ident))?"`
}

// Key joins the parsed identifier parts.
func (c *Clause) Key() string {
	return strings.Join(c.KeyParts, " ")
}

// Value returns the raw string value of the clause.
func (c *Clause) Value() *string {
	return c.RawValue
}

// ParsedDocument is the public API format of a parsed script.
type ParsedDocument struct {
	Lines []ParsedLine
}

// ParsedLine is a simplified line struct.
type ParsedLine struct {
	Action  string
	Target  string
	Clauses []ParsedClause
}

// ParsedClause is a simplified clause struct.
type ParsedClause struct {
	Key   string
	Value *string
}

// ParsePlan lexes and parses a DSL script into an AST.
func ParsePlan(planText string) (*ParsedDocument, error) {
	doc, err := dojoParser.ParseString("", planText)
	if err != nil {
		return nil, fmt.Errorf("plan DSL parse error: %w", err)
	}

	result := &ParsedDocument{}
	for _, l := range doc.Lines {
		pl := ParsedLine{
			Action: l.Action,
			Target: l.Target,
		}
		for _, c := range l.Clauses {
			pc := ParsedClause{
				Key: c.Key(),
			}
			if c.RawValue != nil {
				val := *c.RawValue
				if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
					val = val[1 : len(val)-1]
				}
				pc.Value = &val
			}
			pl.Clauses = append(pl.Clauses, pc)
		}
		result.Lines = append(result.Lines, pl)
	}

	return result, nil
}
