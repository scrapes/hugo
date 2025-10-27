package tplimpl

import (
    "io/ioutil"
    "path/filepath"

    "github.com/gohugoio/hugo/tpl/internal/go_templates/texttemplate/parse"
)

// TemplateVar describes a variable discovered in a template.
type TemplateVar struct {
    Name     string
    Required bool
    IsArray  bool
}

// TemplateAnalysis is the result of analyzing a template.
type TemplateAnalysis struct {
    Vars []TemplateVar
}

// AnalyzeTemplate parses the provided template content using Hugo's template parser
// and returns an analysis of variables referenced by the template.
// name is the template name used for parsing; isText indicates plain text template parsing.
func AnalyzeTemplate(name, content string, isText bool) (TemplateAnalysis, error) {
    // Provide minimal func map so parser recognizes identifiers like "partial".
    funcs := map[string]interface{}{
        // Hugo/common helpers (stubs for parse-time only)
        "partial":       func(string, interface{}) interface{} { return nil },
        "partialCached": func(string, interface{}) interface{} { return nil },
        "return":        func(interface{}) interface{} { return nil },
        "i18n":          func(string, ...interface{}) interface{} { return nil },
        // Builtin-like helpers we often see
        "printf": func(string, ...interface{}) string { return "" },
        "len":    func(interface{}) int { return 0 },
        // Comparators/logic
        "eq":  func(...interface{}) bool { return false },
        "ne":  func(...interface{}) bool { return false },
        "lt":  func(...interface{}) bool { return false },
        "le":  func(...interface{}) bool { return false },
        "gt":  func(...interface{}) bool { return false },
        "ge":  func(...interface{}) bool { return false },
        "and": func(...interface{}) bool { return false },
        "or":  func(...interface{}) bool { return false },
        "not": func(interface{}) bool { return false },
        // Arithmetic shortcuts sometimes present in themes
        "div": func(...interface{}) interface{} { return nil },
        "add": func(...interface{}) interface{} { return nil },
        "sub": func(...interface{}) interface{} { return nil },
        "mul": func(...interface{}) interface{} { return nil },
        "mod": func(...interface{}) interface{} { return nil },
    }
    ns := newTemplateNamespace(funcs)

    // Parse the template into the namespace.
    ts, err := ns.parse(templateInfo{name: name, template: content, isText: isText})
    if err != nil {
        return TemplateAnalysis{}, err
    }

    // Get the parse tree and walk it.
    tree := getParseTree(ts.Template)
    collector := &varCollector{vars: make(map[string]TemplateVar)}
    collector.walkWithPrefix(tree.Root, false, "")

    // Convert to slice.
    out := TemplateAnalysis{}
    for _, v := range collector.vars {
        out.Vars = append(out.Vars, v)
    }
    return out, nil
}

type varCollector struct {
    vars map[string]TemplateVar
}

func (c *varCollector) add(name string, required bool, isArray bool) {
    if name == "" {
        return
    }
    v, found := c.vars[name]
    if !found {
        v = TemplateVar{Name: name}
    }
    // Required if required in any context.
    v.Required = v.Required || required
    // Array if seen as array anywhere.
    v.IsArray = v.IsArray || isArray
    c.vars[name] = v
}

func (c *varCollector) walk(n parse.Node, inConditional bool) {
    c.walkWithPrefix(n, inConditional, "")
}

func (c *varCollector) walkWithPrefix(n parse.Node, inConditional bool, elemPrefix string) {
    if n == nil {
        return
    }
    switch nn := n.(type) {
    case *parse.ListNode:
        for _, ch := range nn.Nodes {
            c.walkWithPrefix(ch, inConditional, elemPrefix)
        }
    case *parse.ActionNode:
        c.walkWithPrefix(nn.Pipe, inConditional, elemPrefix)
    case *parse.PipeNode:
        for _, cmd := range nn.Cmds {
            c.walkWithPrefix(cmd, inConditional, elemPrefix)
        }
    case *parse.CommandNode:
        // Detect partial/partialCached calls: partial "name" .
        if len(nn.Args) > 0 {
            if id, ok := nn.Args[0].(*parse.IdentifierNode); ok {
                if id.Ident == "partial" || id.Ident == "partialCached" {
                    // second arg should be the partial name string
                    if len(nn.Args) > 1 {
                        if sn, ok2 := nn.Args[1].(*parse.StringNode); ok2 {
                            c.loadAndWalkPartial(sn.Text, inConditional)
                        }
                    }
                }
            }
        }
        for _, arg := range nn.Args {
            switch a := arg.(type) {
            case *parse.PipeNode:
                c.walkWithPrefix(a, inConditional, elemPrefix)
            default:
                c.walkWithPrefix(arg, inConditional, elemPrefix)
            }
        }
    case *parse.FieldNode:
        // .Title => ["Title"]
        name := joinIdents(nn.Ident)
        if elemPrefix != "" {
            name = elemPrefix + "." + name
        }
        c.add(name, !inConditional, false)
    case *parse.VariableNode:
        // $var references are not part of the page schema; ignore
    case *parse.ChainNode:
        // (.Site).Title => Field has the tail idents
        name := joinIdents(nn.Field)
        if elemPrefix != "" {
            name = elemPrefix + "." + name
        }
        c.add(name, !inConditional, false)
        c.walkWithPrefix(nn.Node, inConditional, elemPrefix)
    case *parse.IfNode:
        c.walkWithPrefix(nn.Pipe, true, elemPrefix)
        c.walkWithPrefix(nn.List, true, elemPrefix)
        if nn.ElseList != nil {
            c.walkWithPrefix(nn.ElseList, true, elemPrefix)
        }
    case *parse.WithNode:
        c.walkWithPrefix(nn.Pipe, true, elemPrefix)
        c.walkWithPrefix(nn.List, true, elemPrefix)
        if nn.ElseList != nil {
            c.walkWithPrefix(nn.ElseList, true, elemPrefix)
        }
    case *parse.RangeNode:
        // Try to detect ranged-over field => array
        rangedName := ""
        if nn.Pipe != nil && len(nn.Pipe.Cmds) > 0 {
            // The first arg to the first command commonly is the field being ranged over.
            if len(nn.Pipe.Cmds[0].Args) > 0 {
                switch a := nn.Pipe.Cmds[0].Args[0].(type) {
                case *parse.FieldNode:
                    rangedName = joinIdents(a.Ident)
                case *parse.ChainNode:
                    rangedName = joinIdents(a.Field)
                }
            }
        }
        if rangedName != "" {
            c.add(rangedName, !inConditional, true)
        }
        // Element prefix for properties within the ranged collection.
        nextPrefix := elemPrefix
        if rangedName != "" {
            nextPrefix = rangedName + "[]"
        }
        c.walkWithPrefix(nn.List, inConditional, nextPrefix)
        if nn.ElseList != nil {
            c.walkWithPrefix(nn.ElseList, inConditional, nextPrefix)
        }
    case *parse.TemplateNode:
        c.walkWithPrefix(nn.Pipe, inConditional, elemPrefix)
    case *parse.TextNode:
        // ignore
    }
}

// loadAndWalkPartial tries to resolve a partial by common layout paths and walk it.
// We assume the current working directory is the project root or site root.
func (c *varCollector) loadAndWalkPartial(name string, inConditional bool) {
    // Resolve candidate filenames
    candidates := []string{}
    // If name has no extension, try .html
    if filepath.Ext(name) == "" {
        candidates = append(candidates, filepath.Join("layouts", "partials", name+".html"))
    }
    // Try as given inside partials/
    candidates = append(candidates, filepath.Join("layouts", "partials", name))
    // Try direct relative (in case absolute-like passed in)
    candidates = append(candidates, name)

    var content []byte
    for _, cand := range candidates {
        b, err := ioutil.ReadFile(cand)
        if err == nil {
            content = b
            break
        }
    }
    if len(content) == 0 {
        return
    }

    // Parse and walk the partial template locally
    funcs := map[string]interface{}{
        "partial":       func(string, interface{}) interface{} { return nil },
        "partialCached": func(string, interface{}) interface{} { return nil },
        "return":        func(interface{}) interface{} { return nil },
        "i18n":          func(string, ...interface{}) interface{} { return nil },
        "printf":        func(string, ...interface{}) string { return "" },
        "len":           func(interface{}) int { return 0 },
        "eq":            func(...interface{}) bool { return false },
        "ne":            func(...interface{}) bool { return false },
        "lt":            func(...interface{}) bool { return false },
        "le":            func(...interface{}) bool { return false },
        "gt":            func(...interface{}) bool { return false },
        "ge":            func(...interface{}) bool { return false },
        "and":           func(...interface{}) bool { return false },
        "or":            func(...interface{}) bool { return false },
        "not":           func(interface{}) bool { return false },
        "div":           func(...interface{}) interface{} { return nil },
        "add":           func(...interface{}) interface{} { return nil },
        "sub":           func(...interface{}) interface{} { return nil },
        "mul":           func(...interface{}) interface{} { return nil },
        "mod":           func(...interface{}) interface{} { return nil },
    }
    ns := newTemplateNamespace(funcs)
    ts, err := ns.parse(templateInfo{name: name, template: string(content), isText: false})
    if err != nil {
        return
    }
    tree := getParseTree(ts.Template)
    c.walk(tree.Root, inConditional)
}

func joinIdents(idents []string) string {
    if len(idents) == 0 {
        return ""
    }
    // The parse.Ident for FieldNode does not include the leading dot.
    // For schema names we want "Title" or "Site.Title" etc.
    s := idents[0]
    for i := 1; i < len(idents); i++ {
        s += "." + idents[i]
    }
    return s
}






