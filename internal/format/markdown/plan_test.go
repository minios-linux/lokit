package markdown

import (
	"bytes"
	"strings"
	"testing"
)

func TestMarkdownPlanIdentityAndHiddenResources(t *testing.T) {
	source := []byte("See [MiniOS Configurator](/docs/Preconfiguring-MiniOS \"Guide\") and <https://example.test/MiniOS>.\n\n`code` stays.\n\n[ref]: /private/MiniOS\n")
	plan, err := BuildPlan("docs", "sec:3", source)
	if err != nil {
		t.Fatal(err)
	}
	view := plan.ProviderFields()
	if len(view) == 0 {
		t.Fatal("plan has no provider fields")
	}
	for _, field := range view {
		if strings.Contains(field.Source, "/docs/") || strings.Contains(field.Source, "example.test") || strings.Contains(field.Source, "/private/") || strings.Contains(field.Source, "`code`") {
			t.Fatalf("provider field exposes immutable resource: %#v", field)
		}
	}
	rendered, err := plan.RenderExact(plan.OriginalPatches())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, source) {
		t.Fatalf("identity render changed source\nwant: %q\ngot:  %q", source, rendered)
	}
}

func TestMarkdownPlanTranslatesLinkLabelWithoutOwningDestination(t *testing.T) {
	source := []byte("See [MiniOS Configurator](/docs/setup) for details.")
	plan, err := BuildPlan("docs", "sec:0", source)
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	fields := plan.Fields()
	if len(fields) != 3 || fields[1].Kind != FieldLinkLabel {
		t.Fatalf("fields = %#v", fields)
	}
	patches[fields[0].ID] = "Подробнее см. в "
	patches[fields[1].ID] = "Конфигураторе MiniOS"
	patches[fields[2].ID] = "."
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	want := "Подробнее см. в [Конфигураторе MiniOS](/docs/setup)."
	if string(rendered) != want {
		t.Fatalf("rendered = %q, want %q", rendered, want)
	}
}

func TestMarkdownPlanRejectsStructuralPatch(t *testing.T) {
	plan, err := BuildPlan("docs", "sec:0", []byte("Text [label](/safe)."))
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	fields := plan.Fields()
	patches[fields[0].ID] = "[evil](https://evil.example)"
	if _, err := plan.RenderExact(patches); err == nil {
		t.Fatal("provider-authored link was accepted")
	}
	delete(patches, fields[0].ID)
	if _, err := plan.RenderExact(patches); err == nil {
		t.Fatal("missing field patch was accepted")
	}
}

func TestMarkdownPlanProtectsVitePressContainers(t *testing.T) {
	source := []byte("::: danger Check the target device\nDo not erase the wrong disk.\n:::\n")
	plan, err := BuildPlan("docs", "sec:0", source)
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	for _, field := range plan.Fields() {
		if strings.Contains(field.Source, "::: danger") || field.Source == ":::" {
			t.Fatalf("container syntax exposed as mutable field: %#v", field)
		}
		if strings.Contains(field.Source, "Check the target device") {
			patches[field.ID] = "Проверьте целевое устройство"
		} else if strings.Contains(field.Source, "Do not erase") {
			patches[field.ID] = "Не стирайте неправильный диск."
		}
	}
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "::: danger Проверьте целевое устройство") || !strings.Contains(string(rendered), "\n:::\n") {
		t.Fatalf("container structure changed: %q fields=%#v", rendered, plan.Fields())
	}
	for _, field := range plan.Fields() {
		patches[field.ID] = "{{ dangerous }}"
		break
	}
	if _, err := plan.RenderExact(patches); err == nil {
		t.Fatal("VitePress template injection was accepted")
	}
}

func TestMarkdownPlanTranslatesHTMLTextOnly(t *testing.T) {
	source := []byte(`<section class="home"><a href="/private/path"><span>Install MiniOS</span><span>Choose a method.</span></a></section>`)
	plan, err := BuildPlan("docs", "sec:0", source)
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	if len(patches) != 2 {
		t.Fatalf("HTML fields = %#v", plan.Fields())
	}
	for _, field := range plan.Fields() {
		if strings.Contains(field.Source, "href") || strings.Contains(field.Source, "/private/") {
			t.Fatalf("HTML resource exposed: %#v", field)
		}
		if field.Source == "Install MiniOS" {
			patches[field.ID] = "Установить MiniOS"
		} else {
			patches[field.ID] = "Выберите способ."
		}
	}
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	if string(rendered) != `<section class="home"><a href="/private/path"><span>Установить MiniOS</span><span>Выберите способ.</span></a></section>` {
		t.Fatalf("HTML render changed structure: %q", rendered)
	}
}

func TestMarkdownPlanEscapesHTMLTextPatch(t *testing.T) {
	plan, err := BuildPlan("docs", "sec:0", []byte(`<section><span>Safe text</span></section>`))
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	for _, field := range plan.Fields() {
		patches[field.ID] = `<img src=x onerror="alert(1)">`
	}
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rendered), "<img") || !strings.Contains(string(rendered), "&lt;img") {
		t.Fatalf("HTML patch was not escaped as text: %q", rendered)
	}
}

func TestMarkdownPlanDoesNotExtractHTMLFromCode(t *testing.T) {
	for _, source := range [][]byte{
		[]byte("Use `<span>Hello</span>` here."),
		[]byte("Use `<span>` here."),
		[]byte("```html\n<span>Hello</span>\n```"),
		[]byte("```<span>\n```"),
		[]byte("    <span>Hello</span>\n"),
	} {
		plan, err := BuildPlan("docs", "sec:0", source)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range plan.Fields() {
			if strings.Contains(field.Source, "Hello") {
				t.Fatalf("code content became provider-owned: source=%q field=%#v", source, field)
			}
		}
		rendered, err := plan.RenderExact(plan.OriginalPatches())
		if err != nil || !bytes.Equal(rendered, source) {
			t.Fatalf("code identity render: got=%q err=%v", rendered, err)
		}
	}
}

func TestMarkdownPlanKeepsRawHTMLContentsHostOwned(t *testing.T) {
	for _, tag := range []string{"script", "style", "pre", "code"} {
		upper := strings.ToUpper(tag)
		source := []byte("Before <" + upper + " data-kind=\"example\"><span>Hello</span></" + upper + "> after.")
		plan, err := BuildPlan("docs/raw.md", "sec:0", source)
		if err != nil {
			t.Fatal(err)
		}
		visible := ""
		for _, field := range plan.ProviderFields() {
			visible += field.Source
		}
		if strings.Contains(visible, "Hello") || !strings.Contains(visible, "Before") || !strings.Contains(visible, "after") {
			t.Fatalf("raw <%s> ownership is wrong: fields=%#v", tag, plan.Fields())
		}
		rendered, err := plan.RenderExact(plan.OriginalPatches())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rendered, source) {
			t.Fatalf("raw <%s> identity changed: %q", tag, rendered)
		}
	}
}

func TestMarkdownPlanProtectsVitePressExpressions(t *testing.T) {
	source := []byte("Welcome, {{ $frontmatter.user.name }}! Read {{ value.replace(\"}}\", \"\") }} and {{ user ?? `Guest` }} items.")
	plan, err := BuildPlan("docs", "sec:0", source)
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	for _, field := range plan.Fields() {
		if strings.Contains(field.Source, "{{") || strings.Contains(field.Source, "}}") || strings.Contains(field.Source, "$frontmatter") || strings.Contains(field.Source, "value.replace") || strings.Contains(field.Source, "Guest") {
			t.Fatalf("VitePress expression became mutable: %#v", field)
		}
		if strings.Contains(field.Source, "Welcome") {
			patches[field.ID] = "Добро пожаловать, "
		} else if strings.Contains(field.Source, "Read") {
			patches[field.ID] = "! Прочитано "
		} else if strings.Contains(field.Source, "items") {
			patches[field.ID] = " элементов."
		}
	}
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "{{ $frontmatter.user.name }}") || !strings.Contains(string(rendered), `{{ value.replace("}}", "") }}`) || !strings.Contains(string(rendered), "{{ user ?? `Guest` }}") {
		t.Fatalf("VitePress expression changed: %q", rendered)
	}
}

func TestMarkdownPlanIgnoresVitePressDelimitersInCode(t *testing.T) {
	for _, source := range [][]byte{
		[]byte("Use `{{` to open and `}}` to close."),
		[]byte("```text\n{{\n```\n\nFollowing prose."),
		[]byte("    {{\n\nFollowing prose."),
	} {
		plan, err := BuildPlan("docs", "sec:0", source)
		if err != nil {
			t.Fatal(err)
		}
		visible := ""
		for _, field := range plan.ProviderFields() {
			visible += field.Source
		}
		if !strings.Contains(visible, "Following prose") && !strings.Contains(visible, "to open and") {
			t.Fatalf("code delimiter hid surrounding prose: source=%q fields=%#v", source, plan.Fields())
		}
	}
}

func TestMarkdownPlanExpandsImplicitReferences(t *testing.T) {
	for _, source := range []string{
		"Read [Guide][].\n\n[Guide]: /private/path\n",
		"Read [Guide].\n\n[Guide]: /private/path\n",
		"See ![Diagram][].\n\n[Diagram]: /private/image.png\n",
	} {
		plan, err := BuildPlan("docs", "sec:0", []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		patches := plan.OriginalPatches()
		translated := false
		for _, field := range plan.Fields() {
			if field.Source == "Guide" {
				patches[field.ID] = "Руководство"
				translated = true
			} else if field.Source == "Diagram" {
				patches[field.ID] = "Схема"
				translated = true
			}
		}
		if !translated {
			t.Fatalf("reference label was not mutable: %q fields=%#v", source, plan.Fields())
		}
		rendered, err := plan.RenderExact(patches)
		if err != nil {
			t.Fatalf("render implicit reference %q: %v", source, err)
		}
		if !strings.Contains(string(rendered), "][Guide]") && !strings.Contains(string(rendered), "][Diagram]") {
			t.Fatalf("implicit reference was not expanded: %q", rendered)
		}
		if !strings.Contains(string(rendered), "/private/") {
			t.Fatalf("reference destination changed: %q", rendered)
		}
	}
}

func TestMarkdownPlanExpandsFormattedImplicitReference(t *testing.T) {
	source := []byte("Read [Quick *Guide*][].\n\n[Quick *Guide*]: /private/path\n")
	plan, err := BuildPlan("docs", "sec:0", source)
	if err != nil {
		t.Fatal(err)
	}
	patches := plan.OriginalPatches()
	for _, field := range plan.Fields() {
		switch field.Source {
		case "Quick ":
			patches[field.ID] = "Краткое "
		case "Guide":
			patches[field.ID] = "руководство"
		}
	}
	rendered, err := plan.RenderExact(patches)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "[Краткое *руководство*][Quick *Guide*]") || !strings.Contains(string(rendered), "[Quick *Guide*]: /private/path") {
		t.Fatalf("formatted reference expansion failed: %q", rendered)
	}
}
