package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	ErrMissingWorkflowFile       = errors.New("missing_workflow_file")
	ErrWorkflowParse             = errors.New("workflow_parse_error")
	ErrWorkflowFrontMatterNotMap = errors.New("workflow_front_matter_not_a_map")
)

// Definition is the parsed WORKFLOW.md payload.
type Definition struct {
	Path           string
	Config         map[string]any
	PromptTemplate string
}

// Load parses a WORKFLOW.md path into a workflow definition.
func Load(path string) (Definition, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("%w: %s: %v", ErrMissingWorkflowFile, path, err)
	}

	cfg, prompt, err := parse(string(content))
	if err != nil {
		return Definition{}, err
	}

	return Definition{
		Path:           path,
		Config:         cfg,
		PromptTemplate: strings.TrimSpace(prompt),
	}, nil
}

func parse(raw string) (map[string]any, string, error) {
	lines := splitLines(raw)
	if len(lines) == 0 {
		return map[string]any{}, "", nil
	}

	if lines[0] != "---" {
		return map[string]any{}, raw, nil
	}

	frontMatter := make([]string, 0, len(lines))
	idx := 1
	for ; idx < len(lines); idx++ {
		if lines[idx] == "---" {
			idx++
			break
		}
		frontMatter = append(frontMatter, lines[idx])
	}

	if idx > len(lines) {
		idx = len(lines)
	}

	prompt := strings.Join(lines[idx:], "\n")
	front := strings.TrimSpace(strings.Join(frontMatter, "\n"))
	if front == "" {
		return map[string]any{}, prompt, nil
	}

	var root any
	if err := yaml.Unmarshal([]byte(front), &root); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrWorkflowParse, err)
	}

	decoded, ok := normalizeMap(root)
	if !ok {
		return nil, "", ErrWorkflowFrontMatterNotMap
	}

	if decoded == nil {
		decoded = map[string]any{}
	}

	return decoded, prompt, nil
}

func splitLines(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
}

func normalizeMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			if nested, ok := normalizeMap(v); ok {
				out[k] = nested
				continue
			}
			if list, ok := normalizeSlice(v); ok {
				out[k] = list
				continue
			}
			out[k] = v
		}
		return out, true
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			key := fmt.Sprint(k)
			if nested, ok := normalizeMap(v); ok {
				out[key] = nested
				continue
			}
			if list, ok := normalizeSlice(v); ok {
				out[key] = list
				continue
			}
			out[key] = v
		}
		return out, true
	case nil:
		return map[string]any{}, true
	default:
		return nil, false
	}
}

func normalizeSlice(value any) ([]any, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		if nested, ok := normalizeMap(item); ok {
			out = append(out, nested)
			continue
		}
		if nestedList, ok := normalizeSlice(item); ok {
			out = append(out, nestedList)
			continue
		}
		out = append(out, item)
	}
	return out, true
}
