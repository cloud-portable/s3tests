import unittest

from cloud_portable_s3tests.report._mustache import Template, go_escape


def render(src, view):
    return Template(src).render(view)


class TestMustache(unittest.TestCase):
    def test_variables_use_the_go_escape_set(self):
        self.assertEqual(go_escape("""& ' < > " ` = /"""), "&amp; &#39; &lt; &gt; &#34; ` = /")
        self.assertEqual(render("<p>{{x}}</p>", {"x": "<b>&\"'"}), "<p>&lt;b&gt;&amp;&#34;&#39;</p>")
        self.assertEqual(render("{{{x}}}|{{&x}}", {"x": "<"}), "<|<")
        self.assertEqual(render("[{{missing}}]", {}), "[]")
        self.assertEqual(render("{{n}} {{b}}", {"n": 5, "b": True}), "5 true")

    def test_sections_iterate_lists_and_test_booleans(self):
        self.assertEqual(render("{{#items}}<{{.}}>{{/items}}", {"items": ["a", "b"]}), "<a><b>")
        self.assertEqual(render("{{#items}}{{name}},{{/items}}", {"items": [{"name": "x"}, {"name": "y"}]}), "x,y,")
        self.assertEqual(render("{{#on}}yes{{/on}}{{#off}}no{{/off}}", {"on": True, "off": False}), "yes")
        self.assertEqual(render("{{#empty}}x{{/empty}}", {"empty": []}), "")
        self.assertEqual(render("{{#obj}}{{a}}{{/obj}}", {"obj": {"a": "in"}}), "in")

    def test_inverted_sections(self):
        self.assertEqual(render("{{^t}}shown{{/t}}", {"t": ""}), "shown")
        self.assertEqual(render("{{^t}}shown{{/t}}", {"t": []}), "shown")
        self.assertEqual(render("{{^t}}hidden{{/t}}", {"t": "x"}), "")

    def test_dotted_names_and_context_stack(self):
        view = {"counts": {"pass": 3}, "outer": "o", "items": [{"inner": "i"}]}
        self.assertEqual(render("{{counts.pass}}", view), "3")
        self.assertEqual(render("{{#items}}{{inner}}{{outer}}{{counts.pass}}{{/items}}", view), "io3")
        self.assertEqual(render("{{#counts}}{{pass}}{{/counts}}", view), "3")

    def test_standalone_lines_are_removed(self):
        src = "a\n{{#t}}\n  b\n{{/t}}\nc\n"
        self.assertEqual(render(src, {"t": True}), "a\n  b\nc\n")
        self.assertEqual(render(src, {"t": False}), "a\nc\n")
        # Indented standalone tags take their indentation and newline with them.
        self.assertEqual(render("  {{#t}}\n  x\n  {{/t}}\n", {"t": True}), "  x\n")
        # Tags sharing a line with other content are not standalone.
        self.assertEqual(render('class="n{{#z}} zero{{/z}}"\n', {"z": True}), 'class="n zero"\n')
        self.assertEqual(render("v={{v}}{{#z}} z{{/z}}\nnext\n", {"v": 1, "z": False}), "v=1\nnext\n")
        # Comments are standalone-eligible too.
        self.assertEqual(render("a\n{{! note }}\nb\n", {}), "a\nb\n")

    def test_malformed_templates_raise(self):
        with self.assertRaises(ValueError):
            Template("{{#a}}")
        with self.assertRaises(ValueError):
            Template("{{/a}}")


if __name__ == "__main__":
    unittest.main()
