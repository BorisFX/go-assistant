#!/usr/bin/env python3
"""Достаёт текст из офисных документов: договоров, смет, актов КС.

Стратегия зависит от формата:

* .xlsx/.xlsm — openpyxl, ячейка за ячейкой. Смета, пропущенная через PDF,
  теряет правые колонки (цена, сумма) — именно те, ради которых её и читают.
* .docx — python-docx: абзацы и таблицы по порядку.
* .doc/.rtf/.odt/.xls/.ods — LibreOffice в PDF, дальше pdftotext: своих
  парсеров для старых бинарных форматов нет.

Вывод — plain text на stdout.
"""

import argparse
import os
import subprocess
import sys
import tempfile

MAX_CELL = 500
MAX_ROWS_PER_SHEET = 3000
SOFFICE_TIMEOUT = 300


def extract_xlsx(path: str) -> str:
    from openpyxl import load_workbook

    # data_only: нужны посчитанные значения, а не формулы — в смете важна сумма.
    book = load_workbook(path, data_only=True, read_only=True)
    out = []
    for sheet in book.worksheets:
        out.append(f"=== ЛИСТ: {sheet.title} ===")
        rows = 0
        for row in sheet.iter_rows(values_only=True):
            if rows >= MAX_ROWS_PER_SHEET:
                out.append(f"… лист обрезан на {MAX_ROWS_PER_SHEET} строках")
                break
            cells = ["" if v is None else str(v)[:MAX_CELL] for v in row]
            if any(c.strip() for c in cells):
                out.append("\t".join(cells).rstrip())
                rows += 1
        out.append("")
    book.close()
    return "\n".join(out)


def extract_docx(path: str) -> str:
    import docx

    doc = docx.Document(path)
    out = [p.text for p in doc.paragraphs if p.text.strip()]
    for i, table in enumerate(doc.tables, 1):
        out.append(f"=== ТАБЛИЦА {i} ===")
        for row in table.rows:
            cells = [c.text.strip()[:MAX_CELL] for c in row.cells]
            if any(cells):
                out.append("\t".join(cells))
        out.append("")
    return "\n".join(out)


def extract_via_soffice(path: str) -> str:
    """Старые бинарные форматы: конвертация в PDF и обычный pdftotext."""
    with tempfile.TemporaryDirectory() as workdir:
        profile = os.path.join(workdir, "profile")
        proc = subprocess.run(
            [
                "soffice", "--headless", "--norestore", "--invisible",
                # Свой профиль на каждый запуск: иначе параллельные конвертации
                # дерутся за общий, и вторая молча не делает ничего.
                f"-env:UserInstallation=file://{profile}",
                "--convert-to", "pdf", "--outdir", workdir, path,
            ],
            capture_output=True, text=True, timeout=SOFFICE_TIMEOUT,
        )
        produced = [f for f in os.listdir(workdir) if f.lower().endswith(".pdf")]
        if not produced:
            raise RuntimeError(
                f"LibreOffice не смог открыть файл: {proc.stderr.strip()[:300] or proc.stdout.strip()[:300]}"
            )
        pdf = os.path.join(workdir, produced[0])
        text = subprocess.run(
            ["pdftotext", "-enc", "UTF-8", pdf, "-"],
            capture_output=True, text=True, timeout=SOFFICE_TIMEOUT,
        )
        return text.stdout


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("path")
    args = parser.parse_args()

    if not os.path.exists(args.path):
        print(f"файл не найден: {args.path}", file=sys.stderr)
        return 2

    ext = os.path.splitext(args.path)[1].lower()
    try:
        if ext in (".xlsx", ".xlsm"):
            text = extract_xlsx(args.path)
        elif ext == ".docx":
            text = extract_docx(args.path)
        else:
            text = extract_via_soffice(args.path)
    except Exception as err:  # noqa: BLE001 — причина уходит наверх как текст
        print(f"{os.path.basename(args.path)}: {err}", file=sys.stderr)
        return 3

    text = text.strip()
    if not text:
        print(f"{os.path.basename(args.path)}: документ прочитан, но текста в нём нет", file=sys.stderr)
        return 4

    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
