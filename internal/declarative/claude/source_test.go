package claude

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-cli/v2/internal/declarative/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAgentMarkdown(t *testing.T) {
	root := writeTree(t, map[string]string{
		"agents/code-reviewer.md": "---\nmodel: claude-sonnet-5\ndescription: Reviews PRs\n---\n\nReview the diff.\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, "agents/code-reviewer.md")}))

	src := l.Sources()["./agents/code-reviewer.md"]
	require.NotNil(t, src)
	assert.Equal(t, KindAgent, src.Kind)
	assert.Equal(t, "code-reviewer", src.Body["name"], "name defaults to the filename")
	assert.Equal(t, "Review the diff.", src.Body["system"], "the markdown body becomes the system prompt")
	assert.Equal(t, "claude-sonnet-5", src.Body["model"])
	assert.NotContains(t, src.Body, "type")
}

func TestTopLevelDollarKeysAreNotSent(t *testing.T) {
	// `$schema` is there for the editor. A tool's input_schema is JSON Schema,
	// so the same keys nested inside it are part of the declaration.
	const fields = `$schema: https://example.com/agent.json
$comment: owned by infra
model: m
tools:
  - type: custom
    name: lookup
    input_schema:
      $schema: https://json-schema.org/draft/2020-12/schema
      $defs: {id: {type: string}}
      properties: {id: {$ref: "#/$defs/id"}}
`
	root := writeTree(t, map[string]string{
		"agents/a.md":  "---\n" + fields + "---\nprompt\n",
		"agents/b.yml": fields,
		"agents/c.json": `{
  "$schema": "https://example.com/agent.json",
  "$comment": "owned by infra",
  "model": "m",
  "tools": [{
    "type": "custom",
    "name": "lookup",
    "input_schema": {
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "$defs": {"id": {"type": "string"}},
      "properties": {"id": {"$ref": "#/$defs/id"}}
    }
  }]
}`,
		"environments/only-schema.yml": "$schema: https://example.com/environment.json\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))

	for _, key := range []string{"./agents/a.md", "./agents/b.yml", "./agents/c.json"} {
		body, err := planBody(t, l, key)
		require.NoError(t, err, key)
		assert.NotContains(t, body, "$schema", key)
		assert.NotContains(t, body, "$comment", key)
		assert.Equal(t, []any{map[string]any{
			"type": "custom",
			"name": "lookup",
			"input_schema": map[string]any{
				"$schema":    "https://json-schema.org/draft/2020-12/schema",
				"$defs":      map[string]any{"id": map[string]any{"type": "string"}},
				"properties": map[string]any{"id": map[string]any{"$ref": "#/$defs/id"}},
			},
		}}, body["tools"], key)
	}
	assert.NotContains(t, l.Sources(), "./environments/only-schema.yml",
		"a walked file that declares nothing else is passed over, as an empty one is")
}

func TestLoadAgentRejectsDuplicateSystemPrompt(t *testing.T) {
	root := writeTree(t, map[string]string{
		"agents/a.md": "---\nmodel: m\nsystem: from frontmatter\n---\n\nfrom the body\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	err := l.Add(context.Background(), []string{filepath.Join(root, "agents/a.md")})
	require.ErrorContains(t, err, "keep one")
}

func TestKindInference(t *testing.T) {
	root := writeTree(t, map[string]string{
		"environments/cloud.yml":  "description: env\n",
		"deployments/nightly.yml": "description: depl\n",
		"misc/thing.yml":          "type: environment\nname: explicit\n",
		"misc/ambiguous.yml":      "name: what am i\n",
		"skills/demo/SKILL.md":    "---\nname: demo\n---\nbody\n",
	})
	l := core.NewLoader(Registry(), root, nil)

	cases := map[string]core.Kind{
		"environments/cloud.yml":  KindEnvironment,
		"deployments/nightly.yml": KindDeployment,
		"misc/thing.yml":          KindEnvironment,
		"skills/demo/SKILL.md":    KindSkill,
	}
	for path := range cases {
		require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, filepath.FromSlash(path))}), path)
	}

	byKey := map[string]core.Kind{}
	for k, src := range l.Sources() {
		byKey[k] = src.Kind
	}
	for path, want := range cases {
		key := "./" + path
		if want == KindSkill {
			key = "./skills/demo"
		}
		assert.Equal(t, want, byKey[key], path)
	}

	err := l.Add(context.Background(), []string{filepath.Join(root, "misc/ambiguous.yml")})
	require.ErrorContains(t, err, "cannot tell what kind of resource this is")
}

func TestSkillDirectoryIdentity(t *testing.T) {
	root := writeTree(t, map[string]string{
		"skills/pr-writer/SKILL.md":     "---\nname: pr-writer\ndescription: d\n---\nbody\n",
		"skills/pr-writer/ref/notes.md": "notes\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	// Reaching a skill by its directory and by its SKILL.md must yield one
	// resource, not two.
	require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, "skills/pr-writer")}))
	require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, "skills/pr-writer/SKILL.md")}))

	require.Len(t, l.Sources(), 1)
	src := l.Sources()["./skills/pr-writer"]
	require.NotNil(t, src)
	assert.Equal(t, "pr-writer", src.Body["display_name"])
	require.NotNil(t, src.Payload, "a skill carries its files as a payload")
	assert.Equal(t, "pr-writer", src.Payload.(*skillBundle).UploadDir)
}

func TestWalkSkipsFilesItCannotIdentify(t *testing.T) {
	root := writeTree(t, map[string]string{
		"README.md":               "# Not an agent\n",
		"agents/real.md":          "---\nmodel: m\n---\nprompt\n",
		"docs/notes.md":           "just docs\n",
		".github/workflows/x.yml": "on: push\n",
		"node_modules/pkg/a.yml":  "name: nope\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))

	keys := make([]string, 0, len(l.Sources()))
	for k := range l.Sources() {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"./agents/real.md"}, keys,
		"a walk should only pick up files whose kind is unambiguous")
}

func TestExplicitPathStillErrorsOnUnknownKind(t *testing.T) {
	root := writeTree(t, map[string]string{"stuff/thing.yml": "name: x\n"})
	l := core.NewLoader(Registry(), root, nil)
	err := l.Add(context.Background(), []string{filepath.Join(root, "stuff/thing.yml")})
	require.ErrorContains(t, err, "cannot tell what kind")
}

// Discover offers what a directory walk would take, minus what is loaded,
// and nothing else: prose, CI config and stray YAML stay out of the offer.
func TestDiscoverFindsOnlyUntrackedResources(t *testing.T) {
	root := writeTree(t, map[string]string{
		"skills/s/SKILL.md":        "---\nname: s\n---\nbody\n",
		"skills/README.md":         "not a skill\n",
		"agents/a.md":              "---\nmodel: m\n---\na\n",
		"agents/b.md":              "---\nmodel: m\n---\nb\n",
		"agents/README.md":         "# how to add an agent\n",
		"environments/e.yml":       "description: e\n",
		"deployments/schema.yml":   "- a\n- list\n",
		".github/workflows/ci.yml": "name: ci\n",
		"notes.yml":                "title: notes\n",
		"misc/typed.yml":           "type: environment\ndescription: x\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{filepath.Join(root, "agents/a.md")}))

	found, err := l.Discover(root)
	require.NoError(t, err)
	var keys []string
	for _, f := range found {
		keys = append(keys, f.Key+" "+string(f.Kind))
	}
	assert.Equal(t, []string{
		"./skills/s skill",
		"./environments/e.yml environment",
		"./misc/typed.yml environment",
		"./agents/b.md agent",
	}, keys, "dependency order, already-loaded a.md excluded")
	assert.Len(t, l.Sources(), 1, "discovering adds nothing")
}

func TestFilenameNamesTheKind(t *testing.T) {
	root := writeTree(t, map[string]string{
		"review-bot/agent.md":              "---\nmodel: m\n---\nReview.\n",
		"review-bot/environment.yml":       "config: {type: cloud}\n",
		"review-bot/deployment_nightly.md": "---\nagent: ./agent.md\nenvironment_id: ./environment.yml\n---\nGo.\n",
		"review-bot/Deployment-Weekly.yml": "agent: ./agent.md\n",
		"review-bot/notes.md":              "just prose\n",
		"review-bot/ci.yml":                "steps: []\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))

	kinds := map[string]core.Kind{}
	names := map[string]any{}
	for key, src := range l.Sources() {
		kinds[key] = src.Kind
		names[key] = src.Body["name"]
	}
	assert.Equal(t, map[string]core.Kind{
		"./review-bot/agent.md":              KindAgent,
		"./review-bot/environment.yml":       KindEnvironment,
		"./review-bot/deployment_nightly.md": KindDeployment,
		"./review-bot/Deployment-Weekly.yml": KindDeployment,
	}, kinds, "a walk takes files named for a kind and leaves the rest")
	assert.Equal(t, "review-bot", names["./review-bot/agent.md"], "a bare kind filename is named for its directory")
	assert.Equal(t, "nightly", names["./review-bot/deployment_nightly.md"], "the kind prefix is not part of the name")
	assert.Equal(t, "Weekly", names["./review-bot/Deployment-Weekly.yml"], "matching folds case; the name keeps it")
}

func TestKindDirectoryBeatsFilename(t *testing.T) {
	// Inside agents/, a file called deployment_bot.md is an agent with an odd
	// name, not a deployment: the directory already said what lives there.
	root := writeTree(t, map[string]string{
		"agents/deployment_bot.md": "---\nmodel: m\n---\nhi\n",
	})
	l := core.NewLoader(Registry(), root, nil)
	require.NoError(t, l.Add(context.Background(), []string{root}))
	src := l.Sources()["./agents/deployment_bot.md"]
	require.NotNil(t, src)
	assert.Equal(t, KindAgent, src.Kind)
	assert.Equal(t, "deployment_bot", src.Body["name"])
}
