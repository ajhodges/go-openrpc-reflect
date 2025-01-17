package go_openrpc_reflect

import (
	"context"
	"fmt"
	"go/ast"
	"reflect"
	"strings"
	"unicode"

	"github.com/invopop/jsonschema"
	meta_schema "github.com/open-rpc/meta-schema"
)

type EthereumReflectorT struct {
	StandardReflectorT
}

var EthereumReflector = &EthereumReflectorT{}

func (e *EthereumReflectorT) ReceiverMethods(name string, receiver interface{}) ([]meta_schema.MethodOrReference, error) {
	if e.FnReceiverMethods != nil {
		return e.FnReceiverMethods(name, receiver)
	}
	return receiverMethods(e, name, receiver)
}

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// ------------------------------------------------------------------------------

func (e *EthereumReflectorT) IsMethodEligible(method reflect.Method) bool {
	if e.FnIsMethodEligible != nil {
		return e.FnIsMethodEligible(method)
	}
	// Method must be exported.
	if !isExportedMethod(method) {
		return false
	}

	// All arg types are permitted.
	// If context.Context is the first arg type, it will be skipped.

	// Verify return types. The function must return at most one error
	// and/or one other non-error value.
	outs := make([]reflect.Type, method.Func.Type().NumOut())
	for i := 0; i < method.Func.Type().NumOut(); i++ {
		outs[i] = method.Func.Type().Out(i)
	}
	isErrorType := func(ty reflect.Type) bool {
		return ty == errType
	}

	// If an error is returned, it must be the last returned value.
	switch {
	case len(outs) > 2:
		return false
	case len(outs) == 1 && isErrorType(outs[0]):
		return true
	case len(outs) == 2:
		if isErrorType(outs[0]) || !isErrorType(outs[1]) {
			return false
		}
	}
	return true
}

func firstToLower(str string) string {
	ret := []rune(str)
	if len(ret) > 0 {
		ret[0] = unicode.ToLower(ret[0])
	}
	return string(ret)
}

func (e *EthereumReflectorT) GetMethodName(moduleName string, r reflect.Value, m reflect.Method, astFunc *ast.FuncDecl) (string, error) {
	if e.FnGetMethodName != nil {
		return e.FnGetMethodName(moduleName, r, m, astFunc)
	}
	if moduleName == "" {
		ty := r.Type()
		if ty.Kind() == reflect.Ptr {
			ty = ty.Elem()
		}
		moduleName = firstToLower(ty.Name())
	}
	return moduleName + "_" + firstToLower(m.Name), nil
}

func generateJSONSchema(ty reflect.Type) (*meta_schema.JSONSchema, error) {
	reflector := &jsonschema.Reflector{DoNotReference: true}
	schema := reflector.Reflect(reflect.New(ty).Interface())

	marshalledJSON, err := schema.MarshalJSON()
	if err != nil {
		return nil, err
	}

	parsed := &meta_schema.JSONSchema{}
	err = parsed.UnmarshalJSON(marshalledJSON)
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

func (e *EthereumReflectorT) GetMethodParams(r reflect.Value, m reflect.Method, astFunc *ast.FuncDecl) ([]meta_schema.ContentDescriptorObject, error) {
	if e.FnGetMethodParams != nil {
		return e.FnGetMethodParams(r, m, astFunc)
	}
	if astFunc.Type.Params == nil {
		return []meta_schema.ContentDescriptorObject{}, nil
	}

	out := []meta_schema.ContentDescriptorObject{}
	expanded := expandedFieldNamesFromList(astFunc.Type.Params.List)

	for i, field := range expanded {
		ty := m.Type.In(i + 1)

		// Skip context.Context parameter
		if i+1 == 1 && ty == contextType {
			continue
		}

		schema, err := generateJSONSchema(ty)
		if err != nil {
			return nil, err
		}

		name := field.Names[0].Name
		desc, err := e.GetContentDescriptorDescription(r, m, field)
		if err != nil {
			return nil, err
		}

		cd := meta_schema.ContentDescriptorObject{
			Name:        (*meta_schema.ContentDescriptorObjectName)(&name),
			Description: (*meta_schema.ContentDescriptorObjectDescription)(&desc),
			Schema:      schema,
			Required:    (*meta_schema.ContentDescriptorObjectRequired)(new(bool)),
		}
		*cd.Required = true
		out = append(out, cd)
	}
	return out, nil
}

func (e *EthereumReflectorT) GetMethodResult(r reflect.Value, m reflect.Method, astFunc *ast.FuncDecl) (meta_schema.ContentDescriptorObject, error) {
	if e.FnGetMethodResult != nil {
		return e.FnGetMethodResult(r, m, astFunc)
	}
	if astFunc.Type.Results == nil {
		return nullContentDescriptor, nil
	}

	// Check if method returns only error
	if m.Type.NumOut() == 1 && m.Type.Out(0) == errType {
		return nullContentDescriptor, nil
	}

	// Get the first non-error return type
	var resultType reflect.Type
	var resultField *ast.Field
	for i := 0; i < m.Type.NumOut(); i++ {
		if m.Type.Out(i) != errType {
			resultType = m.Type.Out(i)
			if i < len(astFunc.Type.Results.List) {
				resultField = astFunc.Type.Results.List[i]
			}
			break
		}
	}

	if resultType == nil {
		return nullContentDescriptor, nil
	}

	schema, err := generateJSONSchema(resultType)
	if err != nil {
		return nullContentDescriptor, err
	}

	name := resultType.String()
	desc, err := e.GetContentDescriptorDescription(r, m, resultField)
	if err != nil {
		return nullContentDescriptor, err
	}

	cd := meta_schema.ContentDescriptorObject{
		Name:        (*meta_schema.ContentDescriptorObjectName)(&name),
		Description: (*meta_schema.ContentDescriptorObjectDescription)(&desc),
		Schema:      schema,
		Required:    (*meta_schema.ContentDescriptorObjectRequired)(new(bool)),
	}
	*cd.Required = true

	return cd, nil
}

func (r *EthereumReflectorT) GetContentDescriptorDescription(rval reflect.Value, method reflect.Method, field *ast.Field) (string, error) {
	// For parameters and results, use the type name
	if field != nil {
		// Get the type from the field
		ty := field.Type
		switch t := ty.(type) {
		case *ast.Ident:
			// Basic type (e.g., int, string)
			return t.Name, nil
		case *ast.StarExpr:
			// Pointer type
			switch x := t.X.(type) {
			case *ast.Ident:
				if x.Name == "Int" && x.Obj == nil {
					// Special case for *big.Int
					return "*big.Int", nil
				}
				return "*" + x.Name, nil
			case *ast.SelectorExpr:
				// Handle package qualified types (e.g., *big.Int)
				if i, ok := x.X.(*ast.Ident); ok {
					if i.Name == "big" {
						return "*big.Int", nil
					}
					return "*" + x.Sel.Name, nil
				}
			}
		case *ast.ArrayType:
			// Array/slice type
			switch elt := t.Elt.(type) {
			case *ast.Ident:
				return "[]" + elt.Name, nil
			case *ast.SelectorExpr:
				// Handle package qualified types (e.g., []pkg.Type)
				return "[]" + elt.Sel.Name, nil
			}
		case *ast.SelectorExpr:
			// Package qualified type (e.g., big.Int)
			if i, ok := t.X.(*ast.Ident); ok && i.Name == "big" {
				return "big." + t.Sel.Name, nil
			}
			return t.Sel.Name, nil
		}
		return "", fmt.Errorf("unsupported field type: %T", ty)
	}

	// For method descriptions, get from AST if available
	if astFunc, err := getAstFuncDecl(rval, method); err == nil && astFunc != nil {
		if desc := getMethodDescription(method, astFunc); desc != "" {
			return desc, nil
		}
	}

	// Fallback to the method name
	return method.Name, nil
}

// getMethodDescription extracts the documentation comments from an AST function declaration
func getMethodDescription(method reflect.Method, astFunc *ast.FuncDecl) string {
	// If there's no AST function declaration, return empty description
	if astFunc == nil {
		return ""
	}

	// Get only the documentation comments, not the source code
	if astFunc.Doc != nil {
		var desc strings.Builder
		for _, comment := range astFunc.Doc.List {
			// Skip any comment that looks like source code
			if strings.Contains(comment.Text, "func ") {
				continue
			}

			// Remove comment markers and whitespace
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			text = strings.TrimSpace(strings.TrimPrefix(text, "/*"))
			text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))

			if desc.Len() > 0 {
				desc.WriteString("\n")
			}
			desc.WriteString(text)
		}
		return desc.String()
	}

	return ""
}

// getFieldType extracts the type from an AST field
func getFieldType(field *ast.Field) reflect.Type {
	if field == nil {
		return nil
	}

	// Handle different types of AST expressions
	switch t := field.Type.(type) {
	case *ast.Ident:
		// Basic type (e.g., int, string)
		return reflect.TypeOf(t.Name)
	case *ast.StarExpr:
		// Pointer type
		if i, ok := t.X.(*ast.Ident); ok {
			return reflect.PtrTo(reflect.TypeOf(i.Name))
		}
	case *ast.ArrayType:
		// Array/slice type
		if i, ok := t.Elt.(*ast.Ident); ok {
			return reflect.SliceOf(reflect.TypeOf(i.Name))
		}
	}

	return nil
}

func (e *EthereumReflectorT) GetMethodDescription(r reflect.Value, m reflect.Method, astFunc *ast.FuncDecl) (string, error) {
	// Get description from AST if available
	if astFunc != nil {
		if desc := getMethodDescription(m, astFunc); desc != "" {
			return desc, nil
		}
	}
	return m.Name, nil
}
