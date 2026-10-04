# -*- coding: utf-8 -*-
"""The chat template stored in the GGUF files (humanizer/chat_template.jinja) must build exactly the
prompt from humanizer/promptfmt.py. Standard library only; renders with jinja2 when it is installed.

    python3 -m unittest discover -s tests -v
"""
import json
import os
import re
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

from humanizer import promptfmt  # noqa: E402

TEMPLATE = open(os.path.join(ROOT, "humanizer", "chat_template.jinja"), encoding="utf-8").read()


def literal(name):
    m = re.search(r'\{%-\s*set\s+' + name + r'\s*=\s*("(?:[^"\\]|\\.)*")\s*-%\}', TEMPLATE)
    assert m, name
    return json.loads(m.group(1))   # the template only uses \n escapes, which JSON reads the same way


class ChatTemplate(unittest.TestCase):
    def test_strings_match_promptfmt(self):
        self.assertEqual(literal("instr"), promptfmt.INSTR)
        self.assertEqual(literal("sep"), promptfmt.SEP)

    def test_render(self):
        try:
            import jinja2
        except ImportError:
            self.skipTest("jinja2 not installed")
        env = jinja2.Environment(trim_blocks=True, lstrip_blocks=True)   # the settings transformers uses
        t = env.from_string(TEMPLATE)
        draft = "Hi team,\n\nThe review moves to Thursday, 2:00 p.m.\n\nThanks,\nDana"
        cases = [
            [{"role": "user", "content": draft}],
            [{"role": "user", "content": "\n  " + draft + "  \n"}],
            [{"role": "system", "content": "Be nice."}, {"role": "user", "content": draft}],
            [{"role": "user", "content": "old"}, {"role": "assistant", "content": "x"}, {"role": "user", "content": draft}],
            [{"role": "user", "content": [{"type": "text", "text": draft}]}],
        ]
        for msgs in cases:
            self.assertEqual(t.render(messages=msgs, add_generation_prompt=True), promptfmt.build_prompt(draft))
        self.assertEqual(t.render(messages=cases[0], add_generation_prompt=False),
                         promptfmt.build_prompt(draft)[:-len(promptfmt.SEP)])


if __name__ == "__main__":
    unittest.main()
