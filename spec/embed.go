// Package spec loads commands.json, the single definition of every Droidline
// command, and normalises raw requests against it.
package spec

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed commands.json
var commandsJSON []byte

// Raw returns the embedded commands.json bytes.
func Raw() []byte { return commandsJSON }

type I18n map[string]string

// In returns the text for lang, falling back to English.
func (t I18n) In(lang string) string {
	if s := t[lang]; s != "" {
		return s
	}
	return t["en"]
}

type Param struct {
	Ref        string   `json:"ref,omitempty"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Required   bool     `json:"required,omitempty"`
	Default    any      `json:"default,omitempty"`
	Enum       []string `json:"enum,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	ClientOnly bool     `json:"client_only,omitempty"`
	Doc        I18n     `json:"doc,omitempty"`
}

type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type Returns struct {
	Kind   string  `json:"kind"`
	Type   string  `json:"type,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

type Alias struct {
	Name string `json:"name"`
	By   string `json:"by"`
}

type Cut struct {
	Param string `json:"param"`
	When  bool   `json:"when"`
}

type Command struct {
	Name        string   `json:"name"`
	Group       string   `json:"group"`
	Phase       int      `json:"phase"`
	Scope       string   `json:"scope"`
	Requires    []string `json:"requires,omitempty"`
	Condition   bool     `json:"condition,omitempty"`
	CutsNetwork *Cut     `json:"cuts_network,omitempty"`
	Summary     I18n     `json:"summary"`
	Notes       I18n     `json:"notes,omitempty"`
	Params      []Param  `json:"params"`
	Returns     Returns  `json:"returns"`
	Errors      []string `json:"errors,omitempty"`
	Aliases     []Alias  `json:"aliases,omitempty"`
	SDKSave     string   `json:"sdk_save,omitempty"`
	SDKWrap     string   `json:"sdk_wrap,omitempty"`
	SDKManual   bool     `json:"sdk_manual,omitempty"`
	// RunsOn "server" marks a phone command the PC carries out itself, using screenshots and taps.
	RunsOn string `json:"runs_on,omitempty"`
	MCP         *bool    `json:"mcp,omitempty"`
	MinSDK      int      `json:"min_sdk,omitempty"`
	Example     string   `json:"example,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// InMCP reports whether the command is exposed as an MCP tool.
func (c *Command) InMCP() bool {
	if c.Scope == "client" {
		return false
	}
	return c.MCP == nil || *c.MCP
}

// Param returns the parameter called name, or nil.
func (c *Command) Param(name string) *Param {
	for i := range c.Params {
		if c.Params[i].Name == name {
			return &c.Params[i]
		}
	}
	return nil
}

type Selector struct {
	By        string `json:"by"`
	Field     string `json:"field"`
	Match     string `json:"match"`
	Doc       I18n   `json:"doc"`
	Shorthand string `json:"shorthand,omitempty"`
}

type ErrorDef struct {
	Code      string `json:"code"`
	HTTP      int    `json:"http"`
	Retryable bool   `json:"retryable"`
	Msg       I18n   `json:"msg"`
	Doc       I18n   `json:"doc"`
}

type Spec struct {
	Proto        int                `json:"proto"`
	Selectors    []Selector         `json:"selectors"`
	Query        QueryDef           `json:"query"`
	CommonParams map[string]Param   `json:"common_params"`
	Errors       []ErrorDef         `json:"errors"`
	Commands     []*Command         `json:"commands"`
	BatchOnly    []*Command         `json:"batch_only"`
	Types        map[string]TypeDef `json:"types"`

	byName  map[string]*Command
	aliases map[string]aliasTarget
	errors  map[string]*ErrorDef
	batch   map[string]*Command
}

// QueryDef documents the keys a query object accepts; spec/query.go implements them.
type QueryDef struct {
	Doc  I18n `json:"doc"`
	Keys []struct {
		Key  string `json:"key"`
		Type string `json:"type"`
		Doc  I18n   `json:"doc"`
	} `json:"keys"`
}

type TypeDef struct {
	Doc    I18n     `json:"doc,omitempty"`
	Fields []string `json:"fields"`
}

type aliasTarget struct {
	cmd *Command
	by  string
}

// Load parses the embedded commands.json and resolves parameter references.
func Load() (*Spec, error) { return Parse(commandsJSON) }

// MustLoad is Load for package init paths where a broken embed is a build bug.
func MustLoad() *Spec {
	s, err := Load()
	if err != nil {
		panic(err)
	}
	return s
}

func Parse(data []byte) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("commands.json: %w", err)
	}
	s.byName = map[string]*Command{}
	s.aliases = map[string]aliasTarget{}
	s.errors = map[string]*ErrorDef{}
	s.batch = map[string]*Command{}
	for i := range s.Errors {
		s.errors[s.Errors[i].Code] = &s.Errors[i]
	}
	resolve := func(c *Command) error {
		for i, p := range c.Params {
			if p.Ref == "" {
				continue
			}
			base, ok := s.CommonParams[p.Ref]
			if !ok {
				return fmt.Errorf("command %s: unknown param ref %q", c.Name, p.Ref)
			}
			// A ref may override the default and the doc, as find_all does for timeout.
			base.Name = p.Ref
			if p.Default != nil {
				base.Default = p.Default
			}
			if p.Doc != nil {
				base.Doc = p.Doc
			}
			c.Params[i] = base
		}
		return nil
	}
	for _, c := range s.Commands {
		if _, dup := s.byName[c.Name]; dup {
			return nil, fmt.Errorf("duplicate command %s", c.Name)
		}
		if err := resolve(c); err != nil {
			return nil, err
		}
		for _, e := range c.Errors {
			if s.errors[e] == nil {
				return nil, fmt.Errorf("command %s: unknown error %s", c.Name, e)
			}
		}
		s.byName[c.Name] = c
		for _, a := range c.Aliases {
			s.aliases[a.Name] = aliasTarget{cmd: c, by: a.By}
		}
	}
	for _, c := range s.BatchOnly {
		c.Scope = "batch"
		if err := resolve(c); err != nil {
			return nil, err
		}
		s.batch[c.Name] = c
	}
	return &s, nil
}

// Command looks up a command by name or alias. by is non-empty for aliases.
func (s *Spec) Command(name string) (c *Command, by string) {
	if c := s.byName[name]; c != nil {
		return c, ""
	}
	if a, ok := s.aliases[name]; ok {
		return a.cmd, a.by
	}
	return nil, ""
}

func (s *Spec) Error(code string) *ErrorDef { return s.errors[code] }

func (s *Spec) IsSelector(by string) bool {
	for _, sel := range s.Selectors {
		if sel.By == by {
			return true
		}
	}
	return false
}

func (s *Spec) SelectorNames() []string {
	out := make([]string, len(s.Selectors))
	for i, sel := range s.Selectors {
		out[i] = sel.By
	}
	return out
}

// Names returns all command names (not aliases) in definition order.
func (s *Spec) Names() []string {
	out := make([]string, 0, len(s.Commands))
	for _, c := range s.Commands {
		out = append(out, c.Name)
	}
	return out
}

// Similar returns up to n command or alias names close to name, for "did you mean".
func (s *Spec) Similar(name string, n int) []string {
	type cand struct {
		name string
		d    int
	}
	var cs []cand
	add := func(k string) {
		d := levenshtein(strings.ToLower(name), strings.ToLower(k))
		if d <= max(1, len(name)/3) || (len(name) >= 3 && strings.Contains(k, name)) {
			cs = append(cs, cand{k, d})
		}
	}
	for k := range s.byName {
		add(k)
	}
	for k := range s.aliases {
		add(k)
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].name < cs[j].name
	})
	var out []string
	for i := 0; i < len(cs) && i < n; i++ {
		out = append(out, cs[i].name)
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
