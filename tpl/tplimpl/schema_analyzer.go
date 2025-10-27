package tplimpl

import (
    "io/ioutil"
    "path/filepath"
    "regexp"
    "strings"

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
    // Minimal function set, auto-extended based on parse errors.
    funcs := map[string]interface{}{
        // Hugo/common helpers (stubs for parse-time only)
        "return":        func(interface{}) interface{} { return nil },
        // Builtin-like helpers we often see
        "printf": func(string, ...interface{}) string { return "" },
        "len":    func(interface{}) int { return 0 },
        "slice":  func(...interface{}) []interface{} { return nil },
        "in":     func(...interface{}) bool { return false },
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
    // Parse with dynamic stub augmentation for unknown functions.
    ts, err := parseWithDynamicStubs(funcs, name, content, isText)
    if err != nil {
        return TemplateAnalysis{}, err
    }

    // Get the parse tree and walk it.
    tree := getParseTree(ts.Template)
    collector := &varCollector{vars: make(map[string]TemplateVar), funcs: funcs}
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
    funcs map[string]interface{}
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
        // Capture simple {{ .path }} and {{ (.chain).path }} actions.
        if nn.Pipe != nil && len(nn.Pipe.Cmds) > 0 {
            for _, arg := range nn.Pipe.Cmds[0].Args {
                switch a := arg.(type) {
                case *parse.FieldNode:
                    name := joinIdents(a.Ident)
                    if elemPrefix != "" {
                        name = elemPrefix + "." + name
                    }
                    c.add(name, !inConditional, false)
                case *parse.ChainNode:
                    name := pathFromChain(a)
                    if elemPrefix != "" {
                        name = elemPrefix + "." + name
                    }
                    c.add(name, !inConditional, false)
                }
            }
        }
    case *parse.PipeNode:
        // Walk all commands
        for _, cmd := range nn.Cmds {
            c.walkWithPrefix(cmd, inConditional, elemPrefix)
        }
        // Also record final value in pipeline if it's a bare field/chain
        last := nn.Cmds[len(nn.Cmds)-1]
        if len(last.Args) > 0 {
            switch a := last.Args[0].(type) {
            case *parse.FieldNode:
                name := joinIdents(a.Ident)
                if elemPrefix != "" {
                    name = elemPrefix + "." + name
                }
                c.add(name, !inConditional, false)
            case *parse.ChainNode:
                name := pathFromChain(a)
                if elemPrefix != "" {
                    name = elemPrefix + "." + name
                }
                c.add(name, !inConditional, false)
            }
        }
    case *parse.CommandNode:
        // Detect calls with string-literal first arg; avoid magic names by validating signature.
        if len(nn.Args) > 0 {
            if _, ok := nn.Args[0].(*parse.IdentifierNode); ok {
                // Generalized include heuristic: if a string literal argument resolves to a file
                // under layouts/partials (with or without .html), treat it as an include.
                if len(nn.Args) > 1 {
                    if sn, ok2 := nn.Args[1].(*parse.StringNode); ok2 {
                        c.loadAndWalkPartial(sn.Text, inConditional)
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
        // (.image).src or (.headline).i18n => compute full path (base + tail)
        name := pathFromChain(nn)
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

    // Reuse current collector func stubs for consistency
    ns := newTemplateNamespace(c.funcs)
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
    // The parse.Ident for FieldNode may contain indexing like ["key"]. Normalize: .Props["x"].y -> Props.x.y
    norm := make([]string, 0, len(idents))
    bracketRe := regexp.MustCompile(`\[\"([^\"]+)\"\]`)
    for _, seg := range idents {
        // Strip bracketed form into dot key
        if strings.Contains(seg, "[") {
            // turn key["x"]["y"] into key.x.y
            cleaned := bracketRe.ReplaceAllString(seg, ".$1")
            parts := strings.Split(cleaned, ".")
            for _, p := range parts {
                if p == "" {
                    continue
                }
                norm = append(norm, p)
            }
        } else {
            norm = append(norm, seg)
        }
    }
    return strings.Join(norm, ".")
}

// pathFromChain builds a path for a ChainNode by combining its base node and field tail.
func pathFromChain(cn *parse.ChainNode) string {
    base := ""
    switch b := cn.Node.(type) {
    case *parse.FieldNode:
        base = joinIdents(b.Ident)
    case *parse.VariableNode:
        // ignore $vars
        base = ""
    case *parse.PipeNode:
        // embedded pipe as base not handled; fall back to tail only
        base = ""
    default:
        base = ""
    }
    tail := joinIdents(cn.Field)
    if base == "" {
        return tail
    }
    if tail == "" {
        return base
    }
    return base + "." + tail
}

// parseWithDynamicStubs attempts to parse the template; if parser errors reference undefined
// functions, it stubs them and retries a limited number of times to avoid hardcoding magic names.
func parseWithDynamicStubs(funcs map[string]interface{}, name, content string, isText bool) (*templateState, error) {
    // try up to 20 passes to add missing function stubs
    missingRe := regexp.MustCompile(`function \"([^\"]+)\" not defined`)
    for i := 0; i < 20; i++ {
        ns := newTemplateNamespace(funcs)
        ts, err := ns.parse(templateInfo{name: name, template: content, isText: isText})
        if err == nil {
            return ts, nil
        }
        msg := err.Error()
        // find all missing functions in error
        m := missingRe.FindAllStringSubmatch(msg, -1)
        if len(m) == 0 {
            return nil, err
        }
        added := false
        for _, mm := range m {
            if len(mm) > 1 {
                fname := mm[1]
                if _, exists := funcs[fname]; !exists {
                    funcs[fname] = func(...interface{}) interface{} { return nil }
                    added = true
                }
            }
        }
        if !added {
            return nil, err
        }
        // loop and retry
    }
    ns := newTemplateNamespace(funcs)
    return ns.parse(templateInfo{name: name, template: content, isText: isText})
}






