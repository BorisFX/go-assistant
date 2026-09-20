#!/usr/bin/env python3
"""Читает чертёж (.dwg/.dxf) и выдаёт текстовый дайджест для юр-ревью.

Нейросеть не участвует: данные берутся из примитивов. Нативный DWG сначала
конвертируется в DXF через dwg2dxf (GNU LibreDWG), дальше работает ezdxf.

Выдаётся: паспорт файла, состав по слоям, экспликации и ТЭП (тексты, собранные
построчно по координатам), содержимое штампов (атрибуты блоков) и размеры.
Вывод — plain text на stdout, он же уходит в дайджест-воркер.
"""

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict

try:
    import ezdxf
    from ezdxf.document import Drawing
except ImportError:
    print("ezdxf не установлен", file=sys.stderr)
    sys.exit(3)

# Строки ближе этого расстояния по Y считаются одной строкой таблицы: в
# экспликациях подписи набраны отдельными примитивами и выровнены неидеально.
ROW_TOLERANCE = 2.0

# Дальше этого по X — уже другая колонка, а не продолжение фразы.
COLUMN_GAP = 400.0

MAX_TEXTS = 4000


def convert_dwg(path: str, workdir: str) -> str:
    """DWG → DXF. Возвращает путь к DXF."""
    if not shutil.which("dwg2dxf"):
        print("dwg2dxf не найден (нужен пакет libredwg-tools)", file=sys.stderr)
        sys.exit(4)
    out = os.path.join(workdir, os.path.basename(path) + ".dxf")
    proc = subprocess.run(
        ["dwg2dxf", "-o", out, path],
        capture_output=True, text=True, timeout=600,
    )
    # LibreDWG ругается на неподдерживаемые поля (DIMASSOC и подобные), но файл
    # при этом отдаёт — ориентируемся на результат, а не на код возврата.
    if not os.path.exists(out) or os.path.getsize(out) == 0:
        print(f"dwg2dxf не смог прочитать файл: {proc.stderr.strip()[:400]}", file=sys.stderr)
        sys.exit(5)
    return out


# LibreDWG отдаёт кириллицу escape-последовательностями вида \U+0421.
UNICODE_ESCAPE = re.compile(r"\\U\+([0-9A-Fa-f]{4})")

# Форматирующие коды MTEXT: \fArial|b0|i1|c204|p34;  \C256;  \H0.7x;  \W0.9;  \A1;
MTEXT_FORMAT = re.compile(r"\\[ACcFfHQTWpq][^;]*;")

# Дробь/степень: \S2^;  \S1^2;  — содержимое сохраняем, разделители убираем.
MTEXT_STACK = re.compile(r"\\S([^;]*);")

# Одиночные переключатели без точки с запятой.
MTEXT_SWITCH = re.compile(r"\\[LlOoKkXxNn~]")


def clean(text: str) -> str:
    r"""Возвращает человекочитаемый текст примитива.

    Без этого юрист читает не чертёж, а смесь escape-кодов: имена шрифтов и
    цвета в MTEXT вперемешку с кириллицей в виде \U+04XX.
    """
    text = UNICODE_ESCAPE.sub(lambda m: chr(int(m.group(1), 16)), text)
    text = text.replace("\\P", "\n").replace("\\~", " ")
    text = MTEXT_STACK.sub(lambda m: m.group(1).replace("^", "").replace("/", "/"), text)
    text = MTEXT_FORMAT.sub("", text)
    text = MTEXT_SWITCH.sub("", text)
    text = text.replace("%%d", "°").replace("%%c", "Ø").replace("%%p", "±")
    text = text.replace("\\\\", "\\")
    text = re.sub(r"[{}]", "", text)
    return re.sub(r"[ \t]+", " ", text).strip()


def collect_texts(msp):
    """Все текстовые примитивы с координатами."""
    items = []
    for e in msp:
        kind = e.dxftype()
        try:
            if kind == "TEXT":
                pos = e.dxf.insert
                items.append((pos.x, pos.y, clean(e.dxf.text), e.dxf.layer))
            elif kind == "MTEXT":
                pos = e.dxf.insert
                items.append((pos.x, pos.y, clean(e.text), e.dxf.layer))
            elif kind == "INSERT":
                # Штампы — это блоки с атрибутами; в них шифр, стадия, даты, подписи.
                for att in e.attribs:
                    pos = att.dxf.insert
                    value = clean(att.dxf.text)
                    if value:
                        items.append((pos.x, pos.y, f"{att.dxf.tag}: {value}", e.dxf.layer))
        except (AttributeError, ValueError):
            continue
    return items


def group_rows(items):
    """Складывает примитивы в строки: сортировка по Y вниз, внутри строки — по X."""
    rows = defaultdict(list)
    for x, y, text, layer in items:
        if not text:
            continue
        rows[round(y / ROW_TOLERANCE)].append((x, text, layer))

    out = []
    for key in sorted(rows, reverse=True):
        cells = sorted(rows[key])
        line, prev_x = [], None
        for x, text, _ in cells:
            if prev_x is not None and x - prev_x > COLUMN_GAP:
                line.append("|")
            line.append(text)
            prev_x = x
        out.append(" ".join(line))
    return out


def describe_layers(msp) -> list:
    counts = Counter(e.dxf.layer for e in msp if e.dxf.hasattr("layer"))
    return [f"{layer}: {n}" for layer, n in counts.most_common(40)]


def linear_factor(doc, entity) -> float:
    """Множитель DIMLFAC размерного стиля.

    Чертёж часто вычерчен в метрах, а размеры подписаны в миллиметрах: без
    этого коэффициента геометрия «расходится» с подписью в 500-1000 раз, и
    честный размер выглядит как подделанный.
    """
    for source in (override_factor(entity), style_factor(doc, entity)):
        try:
            value = float(source)
        except (TypeError, ValueError):
            continue
        if value:
            return value
    return 1.0


def override_factor(entity):
    """DIMLFAC, переопределённый на самом размере (XDATA группы ACAD)."""
    try:
        return entity.override().get("dimlfac", None)
    except Exception:
        return None


def style_factor(doc, entity):
    try:
        style = doc.dimstyles.get(entity.dxf.dimstyle)
    except Exception:
        return None
    return style.dxf.get("dimlfac", None)


def describe_dimensions(doc, msp) -> list:
    """Размеры по геометрии, приведённые к единицам подписи.

    Смысл в сверке: подпись можно вписать руками какую угодно, а расстояние
    между точками — нет. Расхождение показываем, совпадение не засоряет вывод.
    """
    out = []
    for e in msp:
        if e.dxftype() != "DIMENSION":
            continue
        try:
            measurement = e.get_measurement()
        except Exception:
            continue
        if not isinstance(measurement, (int, float)):
            continue

        value = measurement * linear_factor(doc, e)
        override = clean(e.dxf.text) if e.dxf.hasattr("text") else ""
        if override in ("<>", ""):
            out.append(f"{value:.0f}")
            continue

        digits = re.findall(r"\d+(?:[.,]\d+)?", override)
        signed = float(digits[0].replace(",", ".")) if digits else None
        if signed is not None and abs(signed - value) <= max(1.0, abs(value) * 0.01):
            out.append(f"{override}")
        else:
            # Подпись, не сходящаяся с геометрией даже с учётом масштаба, —
            # либо правленный вручную размер, либо ошибка чертежа.
            out.append(f"⚠ подписано «{override}», по геометрии {value:.0f}")
    return out


def report(doc: Drawing, source: str) -> str:
    msp = doc.modelspace()
    entities = Counter(e.dxftype() for e in msp)
    texts = collect_texts(msp)
    truncated = len(texts) > MAX_TEXTS
    rows = group_rows(texts[:MAX_TEXTS])
    dims = describe_dimensions(doc, msp)

    lines = [
        f"ЧЕРТЁЖ: {os.path.basename(source)}",
        f"Формат DXF: {doc.dxfversion}, единицы: {doc.header.get('$INSUNITS', 'не заданы')}",
        f"Примитивов в модели: {sum(entities.values())}",
        "Состав: " + ", ".join(f"{k}×{v}" for k, v in entities.most_common(12)),
        f"Листов (layouts): {', '.join(doc.layouts.names())}",
        "",
        "СЛОИ (имя: число объектов):",
        "  " + "; ".join(describe_layers(msp)),
        "",
    ]

    if dims:
        shown = dims[:60]
        lines += [
            f"РАЗМЕРЫ ({len(dims)} шт., показаны {len(shown)}; ⚠ — подпись расходится с геометрией):",
            "  " + ", ".join(shown),
            "",
        ]

    stamps = [txt for _, _, txt, _ in texts if ":" in txt and len(txt) < 120]
    if stamps:
        unique = list(dict.fromkeys(stamps))[:40]
        lines += ["ПОЛЯ ШТАМПОВ И БЛОКОВ (атрибуты):", "  " + "; ".join(unique), ""]

    lines.append("ТЕКСТ ЧЕРТЕЖА построчно (экспликации, ТЭП, штампы, примечания):")
    lines += [f"  {row}" for row in rows]
    if truncated:
        lines.append(f"  … показаны первые {MAX_TEXTS} текстовых примитивов из {len(texts)}")
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("path", help="файл .dwg или .dxf")
    args = parser.parse_args()

    if not os.path.exists(args.path):
        print(f"файл не найден: {args.path}", file=sys.stderr)
        return 2

    with tempfile.TemporaryDirectory() as workdir:
        path = args.path
        if path.lower().endswith(".dwg"):
            path = convert_dwg(path, workdir)
        try:
            doc = ezdxf.readfile(path)
        except Exception as err:  # noqa: BLE001 — любая ошибка чтения одинаково фатальна
            print(f"не удалось прочитать чертёж: {err}", file=sys.stderr)
            return 6
        print(report(doc, args.path))
    return 0


if __name__ == "__main__":
    sys.exit(main())
