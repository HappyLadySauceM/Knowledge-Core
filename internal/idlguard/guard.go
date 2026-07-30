package idlguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/thriftgo/parser"
)

func ServiceFiles(root string) ([]string, error) {
	files, err := thriftFiles(root)
	if err != nil {
		return nil, err
	}
	var services []string
	for _, file := range files {
		ast, parseErr := parse(root, file)
		if parseErr != nil {
			return nil, parseErr
		}
		if len(ast.Services) > 0 {
			services = append(services, filepath.ToSlash(file))
		}
	}
	return services, nil
}

func CompareTrees(baseRoot, currentRoot string) error {
	baseFiles, err := thriftFiles(baseRoot)
	if err != nil {
		return err
	}
	var violations []string
	for _, relative := range baseFiles {
		basePath := filepath.Join(baseRoot, relative)
		currentPath := filepath.Join(currentRoot, relative)
		if _, statErr := os.Stat(currentPath); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				violations = append(violations, filepath.ToSlash(relative)+": IDL file was removed")
				continue
			}
			return fmt.Errorf("stat current IDL %s: %w", currentPath, statErr)
		}
		baseAST, parseErr := parse(baseRoot, basePath)
		if parseErr != nil {
			return parseErr
		}
		currentAST, parseErr := parse(currentRoot, currentPath)
		if parseErr != nil {
			return parseErr
		}
		violations = append(violations, compareAST(filepath.ToSlash(relative), baseAST, currentAST)...)
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New("incompatible Thrift changes:\n  - " + strings.Join(violations, "\n  - "))
}

// CompareGit materializes the IDL tree from a git revision into an isolated
// directory before comparing it with the working tree.
func CompareGit(repositoryRoot, baseRevision, currentRoot string) error {
	baseRevision = strings.TrimSpace(baseRevision)
	if baseRevision == "" {
		return errors.New("compare IDL with git: base revision is required")
	}
	absRepository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	absCurrent, err := filepath.Abs(currentRoot)
	if err != nil {
		return fmt.Errorf("resolve current IDL root: %w", err)
	}
	relativeRoot, err := filepath.Rel(absRepository, absCurrent)
	if err != nil || relativeRoot == ".." || strings.HasPrefix(relativeRoot, ".."+string(filepath.Separator)) {
		return errors.New("compare IDL with git: current root must be inside the repository")
	}

	list := exec.Command("git", "-C", absRepository, "ls-tree", "-r", "--name-only", baseRevision, "--", filepath.ToSlash(relativeRoot))
	output, err := list.Output()
	if err != nil {
		return fmt.Errorf("list IDLs at %s: %w", baseRevision, err)
	}
	temporaryRoot, err := os.MkdirTemp("", "knowledge-core-idl-base-")
	if err != nil {
		return fmt.Errorf("create temporary IDL root: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryRoot) }()

	for _, repositoryPath := range strings.Fields(string(output)) {
		if filepath.Ext(repositoryPath) != ".thrift" {
			continue
		}
		show := exec.Command("git", "-C", absRepository, "show", baseRevision+":"+repositoryPath)
		content, showErr := show.Output()
		if showErr != nil {
			return fmt.Errorf("read %s at %s: %w", repositoryPath, baseRevision, showErr)
		}
		relative, relErr := filepath.Rel(relativeRoot, filepath.FromSlash(repositoryPath))
		if relErr != nil {
			return fmt.Errorf("resolve base IDL path %s: %w", repositoryPath, relErr)
		}
		target := filepath.Join(temporaryRoot, relative)
		if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o755); mkdirErr != nil {
			return fmt.Errorf("create base IDL directory: %w", mkdirErr)
		}
		if writeErr := os.WriteFile(target, content, 0o600); writeErr != nil {
			return fmt.Errorf("write base IDL %s: %w", repositoryPath, writeErr)
		}
	}
	return CompareTrees(temporaryRoot, absCurrent)
}

func thriftFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".thrift" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list Thrift files in %s: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

func parse(root, path string) (*parser.Thrift, error) {
	ast, err := parser.ParseFile(path, []string{root, filepath.Dir(path)}, true)
	if err != nil {
		return nil, fmt.Errorf("parse Thrift IDL %s: %w", path, err)
	}
	return ast, nil
}

func compareAST(file string, base, current *parser.Thrift) []string {
	var violations []string
	violations = append(violations, compareStructs(file, "struct", base.Structs, current.Structs)...)
	violations = append(violations, compareStructs(file, "union", base.Unions, current.Unions)...)
	violations = append(violations, compareStructs(file, "exception", base.Exceptions, current.Exceptions)...)
	violations = append(violations, compareServices(file, base.Services, current.Services)...)
	violations = append(violations, compareEnums(file, base.Enums, current.Enums)...)
	violations = append(violations, compareTypedefs(file, base.Typedefs, current.Typedefs)...)
	violations = append(violations, compareConstants(file, base.Constants, current.Constants)...)
	return violations
}

func compareStructs(file, category string, base, current []*parser.StructLike) []string {
	currentByName := make(map[string]*parser.StructLike, len(current))
	for _, item := range current {
		currentByName[item.Name] = item
	}
	var violations []string
	for _, old := range base {
		candidate := currentByName[old.Name]
		if candidate == nil {
			violations = append(violations, fmt.Sprintf("%s: %s %s was removed", file, category, old.Name))
			continue
		}
		violations = append(violations, compareFields(file+": "+category+" "+old.Name, old.Fields, candidate.Fields)...)
	}
	return violations
}

func compareServices(file string, base, current []*parser.Service) []string {
	currentByName := make(map[string]*parser.Service, len(current))
	for _, service := range current {
		currentByName[service.Name] = service
	}
	var violations []string
	for _, oldService := range base {
		service := currentByName[oldService.Name]
		if service == nil {
			violations = append(violations, fmt.Sprintf("%s: service %s was removed", file, oldService.Name))
			continue
		}
		if oldService.Extends != service.Extends {
			violations = append(violations, fmt.Sprintf("%s: service %s changed its parent", file, oldService.Name))
		}
		if apiAnnotationSignature(oldService.Annotations) != apiAnnotationSignature(service.Annotations) {
			violations = append(violations, fmt.Sprintf("%s: service %s changed API annotations", file, oldService.Name))
		}
		functions := make(map[string]*parser.Function, len(service.Functions))
		for _, function := range service.Functions {
			functions[function.Name] = function
		}
		for _, oldFunction := range oldService.Functions {
			function := functions[oldFunction.Name]
			prefix := fmt.Sprintf("%s: service %s method %s", file, oldService.Name, oldFunction.Name)
			if function == nil {
				violations = append(violations, prefix+" was removed")
				continue
			}
			if oldFunction.Oneway != function.Oneway || oldFunction.Void != function.Void || typeSignature(oldFunction.FunctionType) != typeSignature(function.FunctionType) {
				violations = append(violations, prefix+" changed its return or oneway contract")
			}
			if apiAnnotationSignature(oldFunction.Annotations) != apiAnnotationSignature(function.Annotations) {
				violations = append(violations, prefix+" changed API annotations")
			}
			violations = append(violations, compareFields(prefix+" argument", oldFunction.Arguments, function.Arguments)...)
			violations = append(violations, compareFields(prefix+" throws", oldFunction.Throws, function.Throws)...)
		}
	}
	return violations
}

func compareFields(prefix string, base, current []*parser.Field) []string {
	currentByID := make(map[int32]*parser.Field, len(current))
	baseByID := make(map[int32]*parser.Field, len(base))
	for _, field := range current {
		currentByID[field.ID] = field
	}
	for _, field := range base {
		baseByID[field.ID] = field
	}
	var violations []string
	for _, old := range base {
		field := currentByID[old.ID]
		if field == nil {
			violations = append(violations, fmt.Sprintf("%s field %d (%s) was removed", prefix, old.ID, old.Name))
			continue
		}
		if old.Name != field.Name {
			violations = append(violations, fmt.Sprintf("%s field %d was renamed from %s to %s", prefix, old.ID, old.Name, field.Name))
		}
		if old.Requiredness != field.Requiredness {
			violations = append(violations, fmt.Sprintf("%s field %d (%s) changed requiredness", prefix, old.ID, old.Name))
		}
		if typeSignature(old.Type) != typeSignature(field.Type) {
			violations = append(violations, fmt.Sprintf("%s field %d (%s) changed type", prefix, old.ID, old.Name))
		}
		if apiAnnotationSignature(old.Annotations) != apiAnnotationSignature(field.Annotations) {
			violations = append(violations, fmt.Sprintf("%s field %d (%s) changed API annotations", prefix, old.ID, old.Name))
		}
	}
	for _, field := range current {
		if baseByID[field.ID] == nil && field.Requiredness == parser.FieldType_Required {
			violations = append(violations, fmt.Sprintf("%s added required field %d (%s)", prefix, field.ID, field.Name))
		}
	}
	return violations
}

func compareEnums(file string, base, current []*parser.Enum) []string {
	currentByName := make(map[string]*parser.Enum, len(current))
	for _, enum := range current {
		currentByName[enum.Name] = enum
	}
	var violations []string
	for _, oldEnum := range base {
		enum := currentByName[oldEnum.Name]
		if enum == nil {
			violations = append(violations, fmt.Sprintf("%s: enum %s was removed", file, oldEnum.Name))
			continue
		}
		values := make(map[string]int64, len(enum.Values))
		for _, value := range enum.Values {
			values[value.Name] = value.Value
		}
		for _, oldValue := range oldEnum.Values {
			value, exists := values[oldValue.Name]
			if !exists || value != oldValue.Value {
				violations = append(violations, fmt.Sprintf("%s: enum %s value %s was removed or renumbered", file, oldEnum.Name, oldValue.Name))
			}
		}
	}
	return violations
}

func compareTypedefs(file string, base, current []*parser.Typedef) []string {
	currentByName := make(map[string]*parser.Typedef, len(current))
	for _, item := range current {
		currentByName[item.Alias] = item
	}
	var violations []string
	for _, old := range base {
		item := currentByName[old.Alias]
		if item == nil || typeSignature(old.Type) != typeSignature(item.Type) {
			violations = append(violations, fmt.Sprintf("%s: typedef %s was removed or changed", file, old.Alias))
		}
	}
	return violations
}

func compareConstants(file string, base, current []*parser.Constant) []string {
	currentByName := make(map[string]*parser.Constant, len(current))
	for _, item := range current {
		currentByName[item.Name] = item
	}
	var violations []string
	for _, old := range base {
		item := currentByName[old.Name]
		if item == nil || typeSignature(old.Type) != typeSignature(item.Type) || !sameJSON(old.Value, item.Value) {
			violations = append(violations, fmt.Sprintf("%s: constant %s was removed or changed", file, old.Name))
		}
	}
	return violations
}

func typeSignature(value *parser.Type) string {
	if value == nil {
		return "void"
	}
	reference := ""
	if value.Reference != nil {
		reference = value.Reference.Name + "."
	}
	return fmt.Sprintf("%d:%s%s<%s,%s>", value.Category, reference, value.Name, typeSignature(value.KeyType), typeSignature(value.ValueType))
}

func apiAnnotationSignature(annotations parser.Annotations) string {
	items := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		if annotation == nil || !strings.HasPrefix(annotation.Key, "api.") {
			continue
		}
		values := append([]string(nil), annotation.Values...)
		sort.Strings(values)
		items = append(items, annotation.Key+"="+strings.Join(values, "\x00"))
	}
	sort.Strings(items)
	return strings.Join(items, "\x01")
}

func sameJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
