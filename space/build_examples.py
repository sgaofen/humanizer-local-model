#!/usr/bin/env python3
"""Build examples.json for the Space from the public evaluation files in ../eval.

    python3 space/build_examples.py

Each example is a held-out draft from the evaluation set and ONE OF 8 SAMPLES for it from the Q8_0 file
you download (humanizer-12b-Q8_0.gguf, llama.cpp v0.5.0, the app's sampling: temperature 1.0, top-p 0.95,
top-k and min-p off, repetition penalty 1.0, nothing resampled), unedited:
eval/outputs/examples-12b-Q8_0_x8.json, all 8 samples per draft kept. Only whitespace is normalised for display.
We read the samples and picked the one that reads best; a pick must also be clean in the same fact-judge pass
as the evaluation (GLM-5.3, one vote: eval/fidelity/examples-12b-Q8_0_x8_{en,zh}.json; English: severity
"none", facts kept, nothing added, meaning unchanged; Chinese: facts kept, nothing added), and we checked its
numbers and names by hand. Across the whole evaluation set the model does make fact errors.
"""
import json
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
EVAL = HERE.parent / "eval"
RUN = "examples-12b-Q8_0_x8"   # 8 samples per draft from the released Q8_0 file (eval/outputs, eval/fidelity)
sys.path.insert(0, str(HERE))
import hz_text  # noqa: E402

PICKS = [  # (id, case, sample index in the x8 file, genre en, genre zh)
    ("en-email", "email_work_06_luna", 4, "Work email", "英文工作邮件"),
    ("en-review", "review_product_09_sonnet", 0, "Product review", "产品评测"),
    ("en-essay", "essay_student_11_sonnet", 3, "Student essay", "学生作文"),
    ("en-forum", "forum_answer_05_sonnet", 2, "Forum answer", "论坛回答"),
    ("zh-email", "zh_email_15_sonnet", 3, "Chinese work email", "中文工作邮件"),
    ("zh-social", "zh_social_15_glm", 4, "Chinese social post", "中文社交帖"),
]
WRITERS = {"glm": "GLM-5.3", "luna": "GPT-5.6 luna", "sonnet": "Claude Sonnet"}


def tidy(s: str) -> str:
    """Whitespace only: no spaces around line breaks; single line breaks become paragraph breaks
    when the text has no blank lines and no code (some samples were stored with ' \\n ')."""
    s = re.sub(r"[ \t]*\n[ \t]*", "\n", s.strip())
    if "\n\n" not in s and "```" not in s:
        s = s.replace("\n", "\n\n")
    return re.sub(r"\n{3,}", "\n\n", s)


def main():
    drafts = {f.stem: f for f in (EVAL / "drafts").glob("300?/*.txt")}
    outputs = json.load(open(EVAL / f"outputs/{RUN}.json", encoding="utf-8"))
    verdicts = {}
    for lang in ("en", "zh"):
        for r in json.load(open(EVAL / f"fidelity/{RUN}_{lang}.json", encoding="utf-8")):
            verdicts[(r["case"], r["i"])] = r["verdict"]

    out = []
    for ex_id, case, k, genre_en, genre_zh in PICKS:
        draft = tidy(drafts[case].read_text(encoding="utf-8"))
        rewrite = tidy(outputs[case][k]["text"])
        v = verdicts[(case, k)] or {}
        clean = v.get("facts_all_kept") is True and not v.get("added_content")
        if not case.startswith("zh_"):
            clean = clean and not v.get("meaning_changed") and v.get("severity") == "none"
        assert clean, (case, k, v)
        nums = hz_text.numbers(draft)
        out.append({
            "id": ex_id,
            "case": case,
            "sample": k,
            "of": len(outputs[case]),
            "lang": "zh" if case.startswith("zh_") else "en",
            "genre": {"en": genre_en, "zh": genre_zh},
            "writer": WRITERS[case.rsplit("_", 1)[1]],
            "draft": draft,
            "output": rewrite,
            "words": [hz_text.count_words(draft), hz_text.count_words(rewrite)],
            "copy": round(hz_text.copy_rate(draft, rewrite), 2),
            "numbers": [len(nums) - len(hz_text.missing_numbers(draft, rewrite)), len(nums)],
            "judge_clean": clean,
        })
        print(f"{ex_id:10s} {case:24s} #{k} words {out[-1]['words']} copy {out[-1]['copy']} numbers {out[-1]['numbers']}")
    (HERE / "examples.json").write_text(json.dumps(out, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
