package gomplate

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/hairyhenderson/gomplate/data"
)

// Config - values necessary for rendering templates with gomplate.
// Mainly for use by the CLI
type Config struct {
	Input       string
	InputFiles  []string
	InputDir    string
	ExcludeGlob []string
	OutputFiles []string
	OutputDir   string
	OutMode     string

	DataSources       []string
	DataSourceHeaders []string

	LDelim string
	RDelim string

	AdditionalTemplates []string
}

// parse an os.FileMode out of the string, and let us know if it's an override or not...
func (o *Config) getMode() (os.FileMode, bool, error) {
	modeOverride := o.OutMode != ""
	m, err := strconv.ParseUint("0"+o.OutMode, 8, 32)
	if err != nil {
		return 0, false, err
	}
	mode := os.FileMode(m)
	return mode, modeOverride, nil
}

// nolint: gocyclo
func (o *Config) String() string {
	c := "input: "
	if o.Input != "" {
		c += "<arg>"
	} else if o.InputDir != "" {
		c += o.InputDir
	} else if len(o.InputFiles) > 0 {
		c += strings.Join(o.InputFiles, ", ")
	}

	if len(o.ExcludeGlob) > 0 {
		c += "\nexclude: " + strings.Join(o.ExcludeGlob, ", ")
	}

	c += "\noutput: "
	if o.InputDir != "" && o.OutputDir != "." {
		c += o.OutputDir
	} else if len(o.OutputFiles) > 0 {
		c += strings.Join(o.OutputFiles, ", ")
	}

	if o.OutMode != "" {
		c += "\nchmod: " + o.OutMode
	}

	if len(o.DataSources) > 0 {
		c += "\ndatasources: " + strings.Join(o.DataSources, ", ")
	}
	if len(o.DataSourceHeaders) > 0 {
		c += "\ndatasourceheaders: " + strings.Join(o.DataSourceHeaders, ", ")
	}

	if o.LDelim != "{{" {
		c += "\nleft_delim: " + o.LDelim
	}
	if o.RDelim != "}}" {
		c += "\nright_delim: " + o.RDelim
	}

	if len(o.AdditionalTemplates) > 0 {
		c += "\ntemplates: " + strings.Join(o.AdditionalTemplates, ", ")
	}
	return c
}

// gomplate -
type gomplate struct {
	funcMap         template.FuncMap
	leftDelim       string
	rightDelim      string
	templateAliases templateAliases
}

// runTemplate -
func (g *gomplate) runTemplate(t *tplate) error {
	context := &context{}
	tmpl, err := t.toGoTemplate(g)
	if err != nil {
		return err
	}

	switch t.target.(type) {
	case io.Closer:
		if t.target != os.Stdout {
			// nolint: errcheck
			defer t.target.(io.Closer).Close()
		}
	}
	err = tmpl.ExecuteTemplate(t.target, t.name, context)
	return err
}

type templateAliases map[string]string

// newGomplate -
func newGomplate(d *data.Data, leftDelim, rightDelim string, ta templateAliases) *gomplate {
	return &gomplate{
		leftDelim:       leftDelim,
		rightDelim:      rightDelim,
		funcMap:         Funcs(d),
		templateAliases: ta,
	}
}

func parseTemplateArgs(templateArgs []string) (templateAliases, error) {
	ta := templateAliases{}
	for _, templateArg := range templateArgs {
		err := parseTemplateArg(templateArg, ta)
		if err != nil {
			return ta, err
		}
	}
	return ta, nil
}

func parseTemplateArg(templateArg string, ta templateAliases) error {
	parts := strings.SplitN(templateArg, "=", 2)
	path := parts[0]
	alias := ""
	if len(parts) > 1 {
		alias = parts[0]
		path = parts[1]
	}
	switch fi, err := os.Stat(path); {
	case err != nil:
		return err
	case fi.IsDir():
		files, err := ioutil.ReadDir(path)
		if err != nil {
			return err
		}
		prefix := path
		if alias != "" {
			prefix = alias
		}
		for _, f := range files {
			if !f.IsDir() { // one-level only
				ta[filepath.Join(prefix, f.Name())] = filepath.Join(path, f.Name())
			}
		}
	default:
		if alias != "" {
			ta[alias] = path
		} else {
			ta[path] = path
		}
	}
	return nil
}

// RunTemplates - run all gomplate templates specified by the given configuration
func RunTemplates(o *Config) error {
	Metrics = newMetrics()
	defer runCleanupHooks()
	d, err := data.NewData(o.DataSources, o.DataSourceHeaders)
	if err != nil {
		return err
	}
	addCleanupHook(d.Cleanup)
	templates, err := parseTemplateArgs(o.AdditionalTemplates)
	if err != nil {
		return err
	}
	g := newGomplate(d, o.LDelim, o.RDelim, templates)

	return g.runTemplates(o)
}

func (g *gomplate) runTemplates(o *Config) error {
	start := time.Now()
	tmpl, err := gatherTemplates(o)
	Metrics.GatherDuration = time.Since(start)
	if err != nil {
		Metrics.Errors++
		return err
	}
	Metrics.TemplatesGathered = len(tmpl)
	start = time.Now()
	defer func() { Metrics.TotalRenderDuration = time.Since(start) }()
	for _, t := range tmpl {
		tstart := time.Now()
		err := g.runTemplate(t)
		Metrics.RenderDuration[t.name] = time.Since(tstart)
		if err != nil {
			Metrics.Errors++
			return err
		}
		Metrics.TemplatesProcessed++
	}
	return nil
}
