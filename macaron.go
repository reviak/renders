package renders

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html/template"
	"net/http"

	"fmt"
	"github.com/oxtoacart/bpool"
	"gopkg.in/macaron.v1"
	"time"
	"log"
	"io"
	"runtime/debug"
)

const (
	ContentType    = "Content-Type"
	ContentLength  = "Content-Length"
	ContentBinary  = "application/octet-stream"
	ContentPlain   = "text/plain"
	ContentJSON    = "application/json"
	ContentHTML    = "text/html"
	ContentXHTML   = "application/xhtml+xml"
	ContentXML     = "text/xml"
	defaultCharset = "UTF-8"
)

const (
	defaultTplSetName = "DEFAULT"
)

// Provides a temporary buffer to execute templates into and catch errors.
var bufpool *bpool.BufferPool
var templates map[string]*template.Template

// Options is a struct for specifying configuration options for the render.Renderer middleware
type Options struct {
	// Directory to load templates. Default is "templates"
	Directory string
	// Extensions to parse template files from. Defaults to [".tmpl"]
	Extensions []string
	// Funcs is a slice of FuncMaps to apply to the template upon compilation. This is useful for helper functions. Defaults to [].
	Funcs template.FuncMap
	// Appends the given charset to the Content-Type header. Default is "UTF-8".
	Charset string
	// Outputs human readable JSON
	IndentJSON bool
	// Outputs human readable XML
	IndentXML bool
	// Prefixes the JSON output with the given bytes.
	PrefixJSON []byte
	// Prefixes the XML output with the given bytes.
	PrefixXML []byte
	// Allows changing of output to XHTML instead of HTML. Default is "text/html"
	HTMLContentType string
}

func Renderer(options ...Options) macaron.Handler {
	opt := prepareOptions(options)
	cs := prepareCharset(opt.Charset)
	bufpool = bpool.NewBufferPool(64)
	return func(res http.ResponseWriter, req *http.Request, c *macaron.Context) {
		if macaron.Env == macaron.DEV {
			// recompile for easy development
			compile(opt)
		}
		r := &renderer{
			ResponseWriter:  res,
			req:             req,
			t:               templates,
			opt:             opt,
			compiledCharset: cs,
		}
		c.Render = r // questionable assignment
		c.MapTo(r, (*macaron.Render)(nil))
	}
}

func compile(options Options) error {
	var tmplErr error

	if len(options.Funcs) > 0 {
		templates, tmplErr = LoadWithFuncMap(options)
		return tmplErr
	} else {
		templates, tmplErr = Load(options)
		return tmplErr
	}
	return nil
}

func prepareCharset(charset string) string {
	if len(charset) != 0 {
		return "; charset=" + charset
	}

	return "; charset=" + defaultCharset
}

func prepareOptions(options []Options) Options {
	var opt Options
	if len(options) > 0 {
		opt = options[0]
	}

	// Defaults
	if len(opt.Directory) == 0 {
		opt.Directory = "templates"
	}
	if len(opt.Extensions) == 0 {
		opt.Extensions = []string{".html"}
	}
	if len(opt.HTMLContentType) == 0 {
		opt.HTMLContentType = ContentHTML
	}

	return opt
}

type renderer struct {
	http.ResponseWriter
	req             *http.Request
	t               map[string]*template.Template
	opt             Options
	compiledCharset string

	startTime time.Time
}

func (r *renderer) SetResponseWriter(rw http.ResponseWriter) {
	r.ResponseWriter = rw
}

func (r *renderer) JSON(status int, v interface{}) {
	var result []byte
	var err error
	if r.opt.IndentJSON {
		result, err = json.MarshalIndent(v, "", "  ")
	} else {
		result, err = json.Marshal(v)
	}
	if err != nil {
		http.Error(r, err.Error(), 500)
		return
	}

	// json rendered fine, write out the result
	r.Header().Set(ContentType, ContentJSON+r.compiledCharset)
	r.WriteHeader(status)
	if len(r.opt.PrefixJSON) > 0 {
		r.Write(r.opt.PrefixJSON)
	}
	r.Write(result)
}

func (r *renderer) JSONString(v interface{}) (string, error) {
	var result []byte
	var err error
	if r.opt.IndentJSON {
		result, err = json.MarshalIndent(v, "", "  ")
	} else {
		result, err = json.Marshal(v)
	}
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func (r *renderer) HTML(status int, name string, binding interface{}, htmlOpt ...macaron.HTMLOptions) {
	t := r.t[name]
	buf, err := r.execute(t, name, binding)
	//fmt.Println(buf.String())
	if err != nil {
		http.Error(r, err.Error(), http.StatusInternalServerError)
		return
	}

	// template rendered fine, write out the result
	r.Header().Set(ContentType, r.opt.HTMLContentType+r.compiledCharset)
	r.WriteHeader(status)
	io.Copy(r, buf)
	bufpool.Put(buf)
}

func (r *renderer) XML(status int, v interface{}) {
	var result []byte
	var err error
	if r.opt.IndentXML {
		result, err = xml.MarshalIndent(v, "", "  ")
	} else {
		result, err = xml.Marshal(v)
	}
	if err != nil {
		http.Error(r, err.Error(), 500)
		return
	}

	// XML rendered fine, write out the result
	r.Header().Set(ContentType, ContentXML+r.compiledCharset)
	r.WriteHeader(status)
	if len(r.opt.PrefixXML) > 0 {
		r.Write(r.opt.PrefixXML)
	}
	r.Write(result)
}

func (r *renderer) data(status int, contentType string, v []byte) {
	if r.Header().Get(ContentType) == "" {
		r.Header().Set(ContentType, contentType)
	}
	r.WriteHeader(status)
	r.Write(v)
}

func (r *renderer) RawData(status int, v []byte) {
	r.data(status, ContentBinary, v)
}

func (r *renderer) PlainText(status int, v []byte) {
	r.data(status, ContentPlain, v)
}

func (r *renderer) execute(t *template.Template, name string, data interface{}) (*bytes.Buffer, error) {
	buf := bufpool.Get()
	//buf := bufpool.Get().(*bytes.Buffer)
	return buf, t.ExecuteTemplate(buf, name, data)
}

func (r *renderer) addYield(t *template.Template, tplName string, data interface{}) {
	funcs := template.FuncMap{
		"yield": func() (template.HTML, error) {
			buf, err := r.execute(t, tplName, data)
			// return safe html here since we are rendering our own template
			return template.HTML(buf.String()), err
		},
		"current": func() (string, error) {
			return tplName, nil
		},
	}
	t.Funcs(funcs)
}

func (r *renderer) renderBytes(setName, tplName string, data interface{}, htmlOpt ...macaron.HTMLOptions) (*bytes.Buffer, error) {
	//t := r.TemplateSet.Get(setName)
	debug.PrintStack()
	log.Println(fmt.Sprintf("macaron renderer renderBytes: set name: %s, tplName: %s", setName, tplName))
	t := r.t[setName]
	if macaron.Env == macaron.DEV {
		log.Println("macaron renderer renderBytes")
		//opt := r.opt
		//opt.Directory = r.TemplateSet.GetDir(setName)
		//t = r.TemplateSet.Set(setName, &opt)
	}
	if t == nil {
		return nil, fmt.Errorf("html/template: template \"%s\" is undefined", tplName)
	}

	opt := r.prepareHTMLOptions(htmlOpt)

	if len(opt.Layout) > 0 {
		r.addYield(t, tplName, data)
		tplName = opt.Layout
	}

	out, err := r.execute(t, tplName, data)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func (r *renderer) renderHTML(status int, setName, tplName string, data interface{}, htmlOpt ...macaron.HTMLOptions) {
	r.startTime = time.Now()

	out, err := r.renderBytes(setName, tplName, data, htmlOpt...)
	if err != nil {
		http.Error(r, err.Error(), http.StatusInternalServerError)
		return
	}

	r.Header().Set(ContentType, r.opt.HTMLContentType+r.compiledCharset)
	r.WriteHeader(status)

	if _, err := out.WriteTo(r); err != nil {
		out.Reset()
	}
	bufpool.Put(out)
}

//func (r *renderer) HTML(status int, name string, data interface{}, htmlOpt ...macaron.HTMLOptions) {
//	r.renderHTML(status, defaultTplSetName, name, data, htmlOpt...)
//}

func (r *renderer) HTMLSet(status int, setName, tplName string, data interface{}, htmlOpt ...macaron.HTMLOptions) {
	r.renderHTML(status, setName, tplName, data, htmlOpt...)
}

func (r *renderer) HTMLSetBytes(setName, tplName string, data interface{}, htmlOpt ...macaron.HTMLOptions) ([]byte, error) {
	out, err := r.renderBytes(setName, tplName, data, htmlOpt...)
	if err != nil {
		return []byte(""), err
	}
	return out.Bytes(), nil
}

func (r *renderer) HTMLBytes(name string, data interface{}, htmlOpt ...macaron.HTMLOptions) ([]byte, error) {
	return r.HTMLSetBytes(defaultTplSetName, name, data, htmlOpt...)
}

func (r *renderer) HTMLSetString(setName, tplName string, data interface{}, htmlOpt ...macaron.HTMLOptions) (string, error) {
	p, err := r.HTMLSetBytes(setName, tplName, data, htmlOpt...)
	return string(p), err
}

func (r *renderer) HTMLString(name string, data interface{}, htmlOpt ...macaron.HTMLOptions) (string, error) {
	p, err := r.HTMLBytes(name, data, htmlOpt...)
	return string(p), err
}

//func (r *renderer) Data(status int, v []byte) {
//	if r.Header().Get(ContentType) == "" {
//		r.Header().Set(ContentType, ContentBinary)
//	}
//	r.WriteHeader(status)
//	r.Write(v)
//}

// Error writes the given HTTP status to the current ResponseWriter
func (r *renderer) Error(status int, message ...string) {
	r.WriteHeader(status)
	if len(message) > 0 {
		r.Write([]byte(message[0]))
	}
}

func (r *renderer) Status(status int) {
	r.WriteHeader(status)
}

func (r *renderer) prepareHTMLOptions(htmlOpt []macaron.HTMLOptions) macaron.HTMLOptions {
	if len(htmlOpt) > 0 {
		return htmlOpt[0]
	}

	return macaron.HTMLOptions{
		//Layout: r.opt.Layout,
	}
}

func (r *renderer) SetTemplatePath(setName, dir string) {
	if len(setName) == 0 {
		setName = defaultTplSetName
	}
	//opt := r.opt
	//opt.Directory = dir
	//r.TemplateSet.Set(setName, &opt)
	//r.t[path.Join(dir, setName)]
	log.Println("Calling SetTemplatePath")
}

func (r *renderer) HasTemplateSet(name string) bool {
	//return r.TemplateSet.Get(name) != nil
	_, ok := r.t[name]
	return ok
	//return r.TemplateSet.Get(name) != nil
}

func (r *renderer) Redirect(location string, status ...int) {
	code := http.StatusFound
	if len(status) == 1 {
		code = status[0]
	}

	http.Redirect(r, r.req, location, code)
}

func (r *renderer) Template(name string) *template.Template {
	return r.t[name]
}
