package tplimpl

import (
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
    ns := newTemplateNamespace(map[string]interface{}{})

    // Parse the template into the namespace.
    ts, err := ns.parse(templateInfo{name: name, template: content, isText: isText})
    if err != nil {
        return TemplateAnalysis{}, err
    }

    // Get the parse tree and walk it.
    tree := getParseTree(ts.Template)
    collector := &varCollector{
        vars: make(map[string]TemplateVar),
    }
    collector.walk(tree.Root, false)

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
    if n == nil {
        return
    }
    switch nn := n.(type) {
    case *parse.ListNode:
        for _, ch := range nn.Nodes {
            c.walk(ch, inConditional)
        }
    case *parse.ActionNode:
        c.walk(nn.Pipe, inConditional)
    case *parse.PipeNode:
        for _, cmd := range nn.Cmds {
            c.walk(cmd, inConditional)
        }
    case *parse.CommandNode:
        for _, arg := range nn.Args {
            switch a := arg.(type) {
            case *parse.PipeNode:
                c.walk(a, inConditional)
            default:
                c.walk(arg, inConditional)
            }
        }
    case *parse.FieldNode:
        // .Title => ["Title"]
        c.add(joinIdents(nn.Ident), !inConditional, false)
    case *parse.VariableNode:
        // $var references are not part of the page schema; ignore
    case *parse.ChainNode:
        // (.Site).Title => Field has the tail idents
        c.add(joinIdents(nn.Field), !inConditional, false)
        c.walk(nn.Node, inConditional)
    case *parse.IfNode:
        c.walk(nn.Pipe, true)
        c.walk(nn.List, true)
        if nn.ElseList != nil {
            c.walk(nn.ElseList, true)
        }
    case *parse.WithNode:
        c.walk(nn.Pipe, true)
        c.walk(nn.List, true)
        if nn.ElseList != nil {
            c.walk(nn.ElseList, true)
        }
    case *parse.RangeNode:
        // Try to detect ranged-over field => array
        if nn.Pipe != nil && len(nn.Pipe.Cmds) > 0 {
            // The first arg to the first command commonly is the field being ranged over.
            if len(nn.Pipe.Cmds[0].Args) > 0 {
                switch a := nn.Pipe.Cmds[0].Args[0].(type) {
                case *parse.FieldNode:
                    c.add(joinIdents(a.Ident), !inConditional, true)
                case *parse.ChainNode:
                    c.add(joinIdents(a.Field), !inConditional, true)
                }
            }
        }
        c.walk(nn.List, inConditional)
        if nn.ElseList != nil {
            c.walk(nn.ElseList, inConditional)
        }
    case *parse.TemplateNode:
        c.walk(nn.Pipe, inConditional)
    case *parse.TextNode:
        // ignore
    }
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


