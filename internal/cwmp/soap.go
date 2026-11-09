package cwmp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"lab-router/internal/network"
	"sort"
	"strings"
)

const Namespace = "urn:dslforum-org:cwmp-1-0"

type Node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []Node
}

func (n *Node) UnmarshalXML(d *xml.Decoder, s xml.StartElement) error {
	n.Name = s.Name
	n.Attrs = s.Attr
	for {
		t, e := d.Token()
		if e != nil {
			return e
		}
		switch v := t.(type) {
		case xml.StartElement:
			var child Node
			if e = d.DecodeElement(&child, &v); e != nil {
				return e
			}
			n.Children = append(n.Children, child)
		case xml.CharData:
			n.Text += string(v)
		case xml.EndElement:
			return nil
		}
	}
}
func (n Node) Child(name string) Node {
	for _, c := range n.Children {
		if c.Name.Local == name {
			return c
		}
	}
	return Node{}
}
func (n Node) Value(name string) string { return n.Child(name).Text }
func Parse(b []byte) (id string, rpc Node, err error) {
	var n Node
	if err = xml.Unmarshal(b, &n); err != nil {
		return
	}
	if n.Name.Local != "Envelope" || n.Name.Space != "http://schemas.xmlsoap.org/soap/envelope/" {
		err = fmt.Errorf("SOAP envelope inválido")
		return
	}
	id = n.Child("Header").Value("ID")
	body := n.Child("Body")
	if len(body.Children) != 1 {
		err = fmt.Errorf("SOAP body inválido")
		return
	}
	rpc = body.Children[0]
	if rpc.Name.Local != "Fault" && rpc.Name.Space != Namespace {
		err = fmt.Errorf("namespace CWMP não suportado")
	}
	return
}
func esc(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func Envelope(id, body string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><soap-env:Envelope xmlns:soap-env="http://schemas.xmlsoap.org/soap/envelope/" xmlns:soap-enc="http://schemas.xmlsoap.org/soap/encoding/" xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:cwmp="` + Namespace + `"><soap-env:Header><cwmp:ID soap-env:mustUnderstand="1">` + esc(id) + `</cwmp:ID></soap-env:Header><soap-env:Body>` + body + `</soap-env:Body></soap-env:Envelope>`)
}
func faultXML(f *Fault) string {
	detail := ""
	if f.Parameter != "" {
		detail = "<SetParameterValuesFault><ParameterName>" + esc(f.Parameter) + "</ParameterName><FaultCode>" + fmt.Sprint(f.Code) + "</FaultCode><FaultString>" + esc(f.Message) + "</FaultString></SetParameterValuesFault>"
	}
	return fmt.Sprintf(`<soap-env:Fault><faultcode>Client</faultcode><faultstring>CWMP fault</faultstring><detail><cwmp:Fault><FaultCode>%d</FaultCode><FaultString>%s</FaultString>%s</cwmp:Fault></detail></soap-env:Fault>`, f.Code, esc(f.Message), detail)
}
func valuesXML(ps []Parameter) string {
	s := fmt.Sprintf(`<ParameterList soap-enc:arrayType="cwmp:ParameterValueStruct[%d]">`, len(ps))
	for _, p := range ps {
		s += `<ParameterValueStruct><Name>` + esc(p.Name) + `</Name><Value xsi:type="xsd:` + p.Type + `">` + esc(p.Value) + `</Value></ParameterValueStruct>`
	}
	return s + "</ParameterList>"
}
func (m Model) Handle(ctx context.Context, rpc Node) (body string, after func(), err error) {
	ps, e := m.Parameters()
	if e != nil {
		return "", nil, e
	}
	switch rpc.Name.Local {
	case "GetRPCMethods":
		methods := []string{"GetRPCMethods", "GetParameterNames", "GetParameterValues", "SetParameterValues", "Reboot", "FactoryReset"}
		body = fmt.Sprintf(`<cwmp:GetRPCMethodsResponse><MethodList soap-enc:arrayType="xsd:string[%d]">`, len(methods))
		for _, v := range methods {
			body += "<string>" + v + "</string>"
		}
		body += "</MethodList></cwmp:GetRPCMethodsResponse>"
	case "GetParameterValues":
		selected := map[string]Parameter{}
		names := rpc.Child("ParameterNames").Children
		requested := make([]string, 0, len(names))
		for _, n := range names {
			requested = append(requested, strings.TrimPrefix(n.Text, Root))
		}
		m.Service.Store.Record("sessions", "ACS", "GetParameterValues.nomes", truncate(strings.Join(requested, " · "), 1500))
		if len(names) == 0 {
			for _, p := range ps {
				selected[p.Name] = p
			}
		}
		for _, n := range names {
			found := false
			for _, p := range ps {
				if p.Name == n.Text || ((n.Text == "" || strings.HasSuffix(n.Text, ".")) && strings.HasPrefix(p.Name, n.Text)) {
					selected[p.Name] = p
					found = true
				}
			}
			if !found {
				return "", nil, &Fault{9005, "Invalid parameter name", n.Text}
			}
		}
		out := []Parameter{}
		for _, p := range selected {
			out = append(out, p)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		body = "<cwmp:GetParameterValuesResponse>" + valuesXML(out) + "</cwmp:GetParameterValuesResponse>"
	case "GetParameterNames":
		path := rpc.Value("ParameterPath")
		m.Service.Store.Record("sessions", "ACS", "GetParameterNames.caminho", path+" (NextLevel="+rpc.Value("NextLevel")+")")
		next, e := boolean(rpc.Value("NextLevel"))
		if e != nil {
			return "", nil, &Fault{9003, "Invalid NextLevel", ""}
		}
		items := map[string]bool{}
		for _, p := range ps {
			if p.Name == path && !next {
				items[p.Name] = p.Writable
			}
			if !strings.HasPrefix(p.Name, path) || !(path == "" || strings.HasSuffix(path, ".")) {
				continue
			}
			suffix := strings.TrimPrefix(p.Name, path)
			parts := strings.Split(suffix, ".")
			if next {
				if len(parts) > 1 {
					items[path+parts[0]+"."] = false
				} else {
					items[p.Name] = p.Writable
				}
			} else {
				items[p.Name] = p.Writable
				prefix := path
				for _, part := range parts[:len(parts)-1] {
					prefix += part + "."
					items[prefix] = false
				}
			}
		}
		if len(items) == 0 {
			return "", nil, &Fault{9005, "Invalid parameter name", path}
		}
		keys := []string{}
		for k := range items {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		body = fmt.Sprintf(`<cwmp:GetParameterNamesResponse><ParameterList soap-enc:arrayType="cwmp:ParameterInfoStruct[%d]">`, len(keys))
		for _, k := range keys {
			body += fmt.Sprintf("<ParameterInfoStruct><Name>%s</Name><Writable>%t</Writable></ParameterInfoStruct>", esc(k), items[k])
		}
		body += "</ParameterList></cwmp:GetParameterNamesResponse>"
	case "SetParameterValues":
		if len(rpc.Value("ParameterKey")) > 32 {
			return "", nil, &Fault{9003, "Invalid ParameterKey", ""}
		}
		values := []Parameter{}
		for _, v := range rpc.Child("ParameterList").Children {
			value := v.Child("Value")
			typ := ""
			for _, a := range value.Attrs {
				if a.Name.Space == "http://www.w3.org/2001/XMLSchema-instance" && a.Name.Local == "type" {
					_, typ, _ = strings.Cut(a.Value, ":")
					if typ == "" {
						typ = a.Value
					}
				}
			}
			if typ == "" {
				return "", nil, &Fault{9006, "Missing parameter type", v.Value("Name")}
			}
			values = append(values, Parameter{Name: v.Value("Name"), Value: value.Text, Type: typ})
		}
		if len(values) == 0 {
			return "", nil, &Fault{9003, "Empty ParameterList", ""}
		}
		if e = m.Set(ctx, values, rpc.Value("ParameterKey")); e != nil {
			return "", nil, e
		}
		body = "<cwmp:SetParameterValuesResponse><Status>0</Status></cwmp:SetParameterValuesResponse>"
	case "Reboot":
		key := rpc.Value("CommandKey")
		if len(key) > 32 {
			return "", nil, &Fault{9003, "Invalid CommandKey", ""}
		}
		body = "<cwmp:RebootResponse/>"
		after = func() {
			if network.Reboot() == nil {
				m.Service.Store.Event("1 BOOT", "")
				m.Service.Store.Event("M Reboot", key)
				m.Service.Store.Record("audit", "ACS", "logical.reboot", "sucesso")
			} else {
				m.Service.Store.Record("audit", "ACS", "logical.reboot", "falha")
			}
		}
	case "FactoryReset":
		var allowed bool
		m.Service.Store.Get("allow_factory_reset", &allowed)
		if !allowed {
			return "", nil, &Fault{9001, "FactoryReset disabled by local administrator", ""}
		}
		if e = m.Service.FactoryReset(ctx); e != nil {
			return "", nil, &Fault{9002, "Lab reset failed", ""}
		}
		body = "<cwmp:FactoryResetResponse/>"
		after = func() { m.Service.Store.Event("0 BOOTSTRAP", "") }
	default:
		return "", nil, &Fault{9000, "Method not supported", ""}
	}
	return body, after, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
