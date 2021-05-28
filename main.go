package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"text/template"
	"unicode"
)

// todo 1. bug-string []解析出错

var YAPIType = map[string]string{
	"string":  "string",
	"array":   "array",
	"object":  "object",
	"number":  "int",
	"integer": "int",
	"boolean": "bool",
}

var (
	inFile         string
	outFile        string
	classification string
	apiUrlPath     string
)

func init() {
	flag.StringVar(&inFile, "i", "", "in file path, default Stdin")
	flag.StringVar(&outFile, "o", "", "out file path, default Stdout")
	flag.StringVar(&classification, "c", "", "classification in yapi. All will transfer struct if classification is nil")
	flag.StringVar(&apiUrlPath, "p", "", "api's url path in yapi. All will transfer struct if path is nil")
	flag.Parse()
}

const (
	common = `// title: {{.Api.Title}}
// method: {{.Api.Method}}    path: {{.Api.Path}}
{{ define "generateStruct" }}
	{{- if eq (TypeConvert .Type) "array" }}[]{{ end }}struct {
		{{- range $key, $subField := .SubField}}
		{{UpperFirst $key}} {{if or (eq (TypeConvert $subField.Type) "object") (eq (TypeConvert $subField.Type) "array") }}	{{ template "generateStruct" $subField }} {{ else }} {{ TypeConvert $subField.Type }} {{- end}}` +
		"{{- if .IsRequired $key -}} `json:\"{{ $key }}\" binding:\"required\"` // {{.Description}}" +
		"{{- else -}} `json:\"{{ $key }}\"` // {{.Description}}" +
		"{{- end -}}" + `
		{{- end}}
	}
{{- end -}}`
	ReqBodyText = common + `type {{.GetReqStructName}} {{ if .Field}}{{ template "generateStruct" .Field}}{{ end }}
`
	RespBodyText = common + `type {{.GetRespStructName}} {{ if .Field}}{{ template "generateStruct" .Field}}{{ end }}
`
)

type Field struct {
	Type        string            `json:"type"`
	Items       *Field            `json:"items"`      // 当Type为Array时, items不为空，包含了一个Field结构体；当Type为Object时，items为空
	SubField    map[string]*Field `json:"properties"` // 当Type为object时，SubField不为空，key为字段名
	Description string            `json:"description"`
	Required    []string          `json:"required"` // 指明字段是否必须
}

func (f *Field) Array2Object() { // 将Items转为SubField，方便渲染
	if f.Type == "array" {
		if f.Items == nil {
			log.Panicln("check field:\n", f.Items)
		}
		f.SubField = f.Items.SubField
		f.Required = f.Items.Required
		f.Description = f.Items.Description
	}
	m := make(map[string]*Field)
	for key, value := range f.SubField { // 修改字段名
		m[FilterRune(key)] = value
	}
	f.SubField = m
	for _, subF := range f.SubField {
		subF.Array2Object()
	}
}

type Body struct {
	*Field
	Api *Api `json:"-"`
}

func (b *Body) GetReqStructName() string {
	return UpperFirst(b.Api.Path[1:]) + "ReqDto"
}

func (b *Body) GetRespStructName() string {
	return UpperFirst(b.Api.Path[1:]) + "RespRto"
}

func (f *Field) IsRequired(field string) bool {
	for _, v := range f.Required {
		if v == field {
			return true
		}
	}
	return false
}

type Api struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	ReqBody  string `json:"req_body_other"`
	RespBody string `json:"res_body"`
}

func (a *Api) GetReqBody() *Body {
	if a.ReqBody == "" {
		return nil
	}
	var body Body
	err := json.Unmarshal([]byte(a.ReqBody), &body)
	if err != nil {
		log.Panicln(a.ReqBody, err)
	}
	body.Api = a
	return &body
}

func (a *Api) GetRespBody() *Body {
	if a.RespBody == "" {
		return nil
	}
	var body Body
	err := json.Unmarshal([]byte(a.RespBody), &body)
	if err != nil {
		log.Panicln(a.RespBody, err)
	}
	body.Api = a
	return &body
}

func (b *Body) template(base string) string {
	b.Array2Object()
	tmpl, err := template.New("x").Funcs(map[string]interface{}{
		"UpperFirst":  UpperFirst,
		"TypeConvert": TypeConvert,
	}).Parse(base)
	if err != nil {
		log.Panicln(err)
	}

	var bufferReq bytes.Buffer
	err = tmpl.Execute(&bufferReq, b)
	if err != nil {
		log.Panicln(err)
	}
	return bufferReq.String()
}

func (a *Api) TemplateReqStruct() []string {
	var result []string
	if bodyReq := a.GetReqBody(); bodyReq != nil {
		result = append(result, bodyReq.template(ReqBodyText))
	}
	if bodyResp := a.GetRespBody(); bodyResp != nil {
		result = append(result, bodyResp.template(RespBodyText))
	}
	return result
}

func (a *Api) TemplateRespStruct() string {
	return ""
}

type Kind struct {
	Name string `json:"name"`
	Apis []Api  `json:"list"`
}

func (k *Kind) TemplateApis() []string {
	var result []string
	for _, api := range k.Apis {
		if apiUrlPath != "" {
			if apiUrlPath != api.Path {
				continue
			}
		}
		result = append(result, api.TemplateReqStruct()...)
	}
	return result
}

type Kinds []*Kind

func ReadFile(path string) []byte {
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return buf
}

func Unmarshal(apiText []byte) Kinds {
	var result, temp Kinds
	var allKindName []string
	if err := json.Unmarshal(apiText, &temp); err != nil {
		panic(err)
	}

	for _, kind := range temp {
		allKindName = append(allKindName, kind.Name)
		if classification != "" {
			if classification != kind.Name {
				continue
			}
		}
		result = append(result, kind)
	}
	if len(result) == 0 {
		log.Panicln("检查分类名称是否正确。", "当前api中的分类名称包括：", allKindName)
	}
	return result
}

func UpperFirst(word string) string {
	var result string
	if word != "" {
		first := unicode.ToUpper(rune(word[0]))
		result = string(first) + word[1:]
	}
	return result
}

// yapi中类型转成golang类型
func TypeConvert(yapiType string) string {
	if value, exist := YAPIType[yapiType]; exist {
		return value
	} else {
		log.Fatalf("请确认yapi类型(%s)是否注册\n", yapiType)
	}
	return ""
}

// 过滤字符串，例如："* nodeNetwork"，返回："nodeNetwork"
func FilterRune(text string) string {
	c := regexp.MustCompile(`[\w]+`)
	result := c.FindString(text)
	if result == "" {
		log.Panicln("非法参数", text)
	}
	return result
}

func Run() {
	var apiText []byte
	if inFile != "" {
		apiText = ReadFile(inFile)
	} else {
		stat, err := os.Stdin.Stat()
		if err != nil {
			log.Panicln(err)
			return
		}
		if stat.Size() == 0 {
			log.Fatalln("inFile and stdin are empty")
		}
		apiText, _ = ioutil.ReadAll(os.Stdin)
	}

	var out []string
	kinds := Unmarshal(apiText)
	for _, k := range kinds {
		out = append(out, k.TemplateApis()...)
	}

	var outWriter io.WriteCloser
	if outFile != "" {
		f, err := os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			log.Panicln(err)
		} else {
			outWriter = f
		}
	} else {
		outWriter = os.Stdout
	}
	for _, item := range out {
		outWriter.Write([]byte(item))
		outWriter.Write([]byte("\n\n"))
	}
	outWriter.Close()
}

func main() {
	Run()
}
