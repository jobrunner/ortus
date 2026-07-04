#!/usr/bin/env python3
"""Romanize non-Latin place names into `name`, preserving the original in `name_native`
and recording the method in `name_source`. See docs/reference/romanization.md.

This pass implements the CYRILLIC systems (the 110k-row bulk); other scripts are added
in later increments. Deterministic, documented per-language systems (national/UNGEGN),
so the result is reproducible and citable.

Usage:
  python3 scripts/romanize.py --validate   # dry-run: compare translit vs OSM name:en, report agreement
  python3 scripts/romanize.py --apply       # add columns name_native/name_source, write romanized name
"""
import argparse, collections, sqlite3, sys, unicodedata

G = "output/osm-admin-places.gpkg"  # default; override with --gpkg

# ---- Unicode script detection (same buckets as scripts/script_census.py) ----
def script_of_char(ch):
    o = ord(ch)
    if (0x41 <= o <= 0x24F) or (0x1E00 <= o <= 0x1EFF): return "Latin"
    if (0x370 <= o <= 0x3FF) or (0x1F00 <= o <= 0x1FFF): return "Greek"
    if (0x400 <= o <= 0x52F): return "Cyrillic"
    if (0x530 <= o <= 0x58F): return "Armenian"
    if (0x10A0 <= o <= 0x10FF): return "Georgian"
    if (0x590 <= o <= 0x5FF): return "Hebrew"
    if (0x600 <= o <= 0x6FF) or (0x750 <= o <= 0x77F) or (0x8A0 <= o <= 0x8FF) \
       or (0xFB50 <= o <= 0xFDFF) or (0xFE70 <= o <= 0xFEFF): return "Arabic"
    if (0x4E00 <= o <= 0x9FFF) or (0x3400 <= o <= 0x4DBF): return "Han"
    if (0x3040 <= o <= 0x30FF): return "Kana"
    if (0xAC00 <= o <= 0xD7AF) or (0x1100 <= o <= 0x11FF): return "Hangul"
    return None

def script_of_name(s):
    if not s: return "EMPTY"
    c = collections.Counter(sc for ch in s if (sc := script_of_char(ch)))
    return c.most_common(1)[0][0] if c else "NONALPHA"

_BOUNDARY = set(" \t\n\r /()[]{},.;:·«»\"") | set("-–—‑")  # NB: apostrophes are NOT boundaries
def is_word_start(s, i):
    # A word start is the string start or after whitespace/hyphen/punctuation — but NOT
    # after an apostrophe (UA: після апострофа стоїть не-ініціальна форма, e.g. Слов'янськ→Sloviansk).
    return i == 0 or s[i-1] in _BOUNDARY

def apply_case(src_upper, piece):
    return piece[:1].upper() + piece[1:] if (src_upper and piece) else piece

def titlecase(s):
    # For unicameral scripts (Georgian, Arabic, Hebrew): capitalize the first alpha of each
    # word so a proper-noun label reads correctly (word starts = string start / after a boundary).
    out = []
    for j, ch in enumerate(s):
        out.append(ch.upper() if (ch.isalpha() and (j == 0 or s[j-1] in _BOUNDARY)) else ch)
    return "".join(out)

# ---- Cyrillic systems (lowercase keys; case re-applied from source) ----
UK = {  # Ukrainian National 2010 (UNGEGN 2012)
    "а":"a","б":"b","в":"v","г":"h","ґ":"g","д":"d","е":"e","ж":"zh","з":"z","и":"y",
    "і":"i","к":"k","л":"l","м":"m","н":"n","о":"o","п":"p","р":"r","с":"s","т":"t",
    "у":"u","ф":"f","х":"kh","ц":"ts","ч":"ch","ш":"sh","щ":"shch","ь":"","'":"","’":"",
}
UK_POS = {"є":("ye","ie"),"ї":("yi","i"),"й":("y","i"),"ю":("yu","iu"),"я":("ya","ia")}

BG = {  # Bulgarian Streamlined System 2009
    "а":"a","б":"b","в":"v","г":"g","д":"d","е":"e","ж":"zh","з":"z","и":"i","й":"y",
    "к":"k","л":"l","м":"m","н":"n","о":"o","п":"p","р":"r","с":"s","т":"t","у":"u",
    "ф":"f","х":"h","ц":"ts","ч":"ch","ш":"sh","щ":"sht","ъ":"a","ь":"y","ю":"yu","я":"ya",
}

RU = {  # BGN/PCGN 1947 (simplified: soft/hard signs omitted; ё->ye/e like e)
    "а":"a","б":"b","в":"v","г":"g","д":"d","ж":"zh","з":"z","и":"i","й":"y","к":"k",
    "л":"l","м":"m","н":"n","о":"o","п":"p","р":"r","с":"s","т":"t","у":"u","ф":"f",
    "х":"kh","ц":"ts","ч":"ch","ш":"sh","щ":"shch","ъ":"","ы":"y","ь":"","э":"e",
    "ю":"yu","я":"ya",
}
RU_E_PREV = set("аеёиоуыэюяй")  # е/ё -> ye/yë after these / at word start, else e/ë

BE = {  # Belarusian national 2007 (UNGEGN); diacritics
    "а":"a","б":"b","в":"v","г":"h","д":"d","ж":"ž","з":"z","і":"i","й":"j","к":"k",
    "л":"l","м":"m","н":"n","о":"o","п":"p","р":"r","с":"s","т":"t","у":"u","ў":"ŭ",
    "ф":"f","х":"ch","ц":"c","ч":"č","ш":"š","ы":"y","ь":"","э":"e","'":"","’":"",
}
BE_POS = {"е":("je","ie"),"ё":("jo","io"),"ю":("ju","iu"),"я":("ja","ia")}
BE_PREV = set("аеёіоуыэюяў")  # je/jo... at word start or after vowel/ў, else ie/io...

SR = {  # Serbian/Montenegrin/Bosnian-Serb Cyrillic -> official Latin (Gaj)
    "а":"a","б":"b","в":"v","г":"g","д":"d","ђ":"đ","е":"e","ж":"ž","з":"z","и":"i",
    "ј":"j","к":"k","л":"l","љ":"lj","м":"m","н":"n","њ":"nj","о":"o","п":"p","р":"r",
    "с":"s","т":"t","ћ":"ć","у":"u","ф":"f","х":"h","ц":"c","ч":"č","џ":"dž","ш":"š",
}
MK = {  # Macedonian romanization — DIGRAPH convention (passport/common; no diacritics)
    "а":"a","б":"b","в":"v","г":"g","д":"d","ѓ":"gj","е":"e","ж":"zh","з":"z","ѕ":"dz",
    "и":"i","ј":"j","к":"k","л":"l","љ":"lj","м":"m","н":"n","њ":"nj","о":"o","п":"p",
    "р":"r","с":"s","т":"t","ќ":"kj","у":"u","ф":"f","х":"h","ц":"c","ч":"ch","џ":"dj","ш":"sh",
}

def _simple(s, table):
    out = []
    for ch in s:
        low = ch.lower(); up = ch.isupper()
        out.append(apply_case(up, table.get(low, ch if script_of_char(ch) is None else low)))
    return "".join(out)

def tr_uk(s):
    out = []
    for i, ch in enumerate(s):
        low = ch.lower(); up = ch.isupper()
        if low == "з" and i+1 < len(s) and s[i+1].lower() == "г":
            out.append(apply_case(up, "zgh")); continue
        if i > 0 and s[i-1].lower() == "з" and low == "г":
            continue  # already emitted as part of zgh
        if low in UK_POS:
            piece = UK_POS[low][0] if is_word_start(s, i) else UK_POS[low][1]
        elif low in UK:
            piece = UK[low]
        else:
            piece = ch if script_of_char(ch) is None else low
        out.append(apply_case(up, piece))
    return "".join(out)

def tr_bg(s):
    r = _simple(s, BG)
    # word-final -ия -> -ia (drop the y): applied on the romanized string endings "iya"
    import re
    r = re.sub(r"iya\b", "ia", r)
    r = re.sub(r"Iya\b", "Ia", r)
    return r

def tr_ru(s):
    out = []
    for i, ch in enumerate(s):
        low = ch.lower(); up = ch.isupper()
        if low in ("е", "ё"):
            base = "ye" if low == "е" else "yë"
            alt = "e" if low == "е" else "ë"
            prev = s[i-1].lower() if i > 0 else ""
            piece = base if (is_word_start(s, i) or prev in RU_E_PREV) else alt
        else:
            piece = RU.get(low, ch if script_of_char(ch) is None else low)
        out.append(apply_case(up, piece))
    return "".join(out)

BE_SOFT = {"l":"ĺ","n":"ń","s":"ś","z":"ź","c":"ć","L":"Ĺ","N":"Ń","S":"Ś","Z":"Ź","C":"Ć"}
def tr_be(s):
    out = []
    for i, ch in enumerate(s):
        low = ch.lower(); up = ch.isupper()
        if low in ("ь", "'", "’"):
            # soft sign: palatalize the preceding l/n/s/z/c into its acute form (2007 system)
            if out and out[-1] in BE_SOFT:
                out[-1] = BE_SOFT[out[-1]]
            continue  # otherwise dropped
        if low in BE_POS:
            prev = s[i-1].lower() if i > 0 else ""
            initial = is_word_start(s, i) or prev in BE_PREV or prev in ("'", "’")
            piece = BE_POS[low][0] if initial else BE_POS[low][1]
        else:
            piece = BE.get(low, ch if script_of_char(ch) is None else low)
        out.append(apply_case(up, piece))
    return "".join(out)

# ---- Georgian (national 2002, UNGEGN; unicameral, no positional rules) ----
KA = {
    "ა":"a","ბ":"b","გ":"g","დ":"d","ე":"e","ვ":"v","ზ":"z","თ":"t","ი":"i","კ":"k",
    "ლ":"l","მ":"m","ნ":"n","ო":"o","პ":"p","ჟ":"zh","რ":"r","ს":"s","ტ":"t","უ":"u",
    "ფ":"p","ქ":"k","ღ":"gh","ყ":"q","შ":"sh","ჩ":"ch","ც":"ts","ძ":"dz","წ":"ts",
    "ჭ":"ch","ხ":"kh","ჯ":"j","ჰ":"h",
}
def tr_ka(s):
    # Georgian is unicameral (no capitals) -> title-case each word for a proper Latin label.
    return titlecase("".join(KA.get(ch, ch) for ch in s))

# ---- Armenian (BGN/PCGN 1981, Eastern; ե->ye / ո->vo word-initial, ու->u, և->ev) ----
HY = {
    "ա":"a","բ":"b","գ":"g","դ":"d","զ":"z","է":"e","ը":"ë","թ":"t","ժ":"zh","ի":"i",
    "լ":"l","խ":"kh","ծ":"ts","կ":"k","հ":"h","ձ":"dz","ղ":"gh","ճ":"ch","մ":"m",
    "յ":"y","ն":"n","շ":"sh","չ":"ch","պ":"p","ջ":"j","ռ":"r","ս":"s","վ":"v","տ":"t",
    "ր":"r","ց":"ts","փ":"p","ք":"k","օ":"o","ֆ":"f",
}
def tr_hy(s):
    out = []; i = 0
    while i < len(s):
        ch = s[i]; low = ch.lower(); up = ch != low and ch.isupper()
        two = (low + (s[i+1].lower() if i+1 < len(s) else ""))
        if two == "ու":
            out.append(apply_case(up, "u")); i += 2; continue
        if low == "և":
            out.append(apply_case(up, "ev")); i += 1; continue
        if low == "ե":
            out.append(apply_case(up, "ye" if is_word_start(s, i) else "e")); i += 1; continue
        if low == "ո":
            out.append(apply_case(up, "vo" if is_word_start(s, i) else "o")); i += 1; continue
        out.append(apply_case(up, HY.get(low, ch if script_of_char(ch) is None else low))); i += 1
    return "".join(out)

# ---- Greek (ELOT 743 / UN / ISO 843) ----
GR = {
    "α":"a","β":"v","γ":"g","δ":"d","ε":"e","ζ":"z","η":"i","θ":"th","ι":"i","κ":"k",
    "λ":"l","μ":"m","ν":"n","ξ":"x","ο":"o","π":"p","ρ":"r","σ":"s","ς":"s","τ":"t",
    "υ":"y","φ":"f","χ":"ch","ψ":"ps","ω":"o",
}
GR_VOICED = set("αειουηωβγδζλμνρ")  # triggers av/ev/iv (before vowel or voiced consonant)
def _strip_accents(s):
    return "".join(c for c in unicodedata.normalize("NFD", s) if not unicodedata.combining(c))
def tr_el(s):
    s = _strip_accents(s); low = s.lower(); out = []; i = 0; n = len(low)
    while i < n:
        c = low[i]; nxt = low[i+1] if i+1 < n else ""; up = s[i].isupper()
        pair = c + nxt
        if pair == "ου": piece, adv = "ou", 2
        elif c in "αεη" and nxt == "υ":
            after = low[i+2] if i+2 < n else ""
            v = {"α": ("av", "af"), "ε": ("ev", "ef"), "η": ("iv", "if")}[c]
            piece, adv = (v[0] if after in GR_VOICED else v[1]), 2
        elif pair == "γγ": piece, adv = "ng", 2
        elif pair == "γκ": piece, adv = "gk", 2
        elif pair == "γξ": piece, adv = "nx", 2
        elif pair == "γχ": piece, adv = "nch", 2
        elif pair == "μπ": piece, adv = ("b" if is_word_start(low, i) else "mp"), 2
        elif pair == "ντ": piece, adv = "nt", 2
        else: piece, adv = GR.get(c, c if script_of_char(c) is None else c), 1
        out.append(apply_case(up, piece)); i += adv
    return "".join(out)

# ---- Arabic (BGN/PCGN, LAST-RESORT machine fallback) ----
# Arabic omits short vowels, so this is inherently approximate (see docs/reference/romanization.md
# "Arabic caveat"): curated osm-en/osm-fr/GNS/GeoNames are preferred; a row tagged translit-ar-bgn
# is a machine guess. Consonantal skeleton + long vowels only; harakat usually absent in OSM data.
AR = {
    "ا":"a","أ":"a","إ":"i","آ":"a","ٱ":"a","ب":"b","ت":"t","ث":"th","ج":"j","ح":"ḥ","خ":"kh",
    "د":"d","ذ":"dh","ر":"r","ز":"z","س":"s","ش":"sh","ص":"ṣ","ض":"ḍ","ط":"ṭ","ظ":"ẓ",
    "ع":"ʻ","غ":"gh","ف":"f","ق":"q","ك":"k","ل":"l","م":"m","ن":"n","ه":"h","ة":"h",
    "و":"w","ي":"y","ى":"á","ء":"ʼ","ؤ":"ʼ","ئ":"ʼ","ک":"k","ی":"y","گ":"g","چ":"ch","پ":"p","ژ":"zh",
    "َ":"a","ِ":"i","ُ":"u","ً":"an","ٍ":"in","ٌ":"un","ّ":"","ْ":"","ٓ":"","ـ":"",  # harakat + tatweel
    "٠":"0","١":"1","٢":"2","٣":"3","٤":"4","٥":"5","٦":"6","٧":"7","٨":"8","٩":"9",
}
def tr_ar(s):
    out = []; i = 0; n = len(s)
    while i < n:
        # definite article الـ at a word start -> "al-" (no sun-letter assimilation; approximate)
        if s[i] in "اٱ" and i+1 < n and s[i+1] == "ل" and is_word_start(s, i) \
           and i+2 < n and script_of_char(s[i+2]) == "Arabic" and s[i+2] not in "اٱ":
            out.append("al-"); i += 2; continue
        ch = s[i]
        out.append(AR.get(ch, ch if script_of_char(ch) is None else ""))
        i += 1
    return titlecase("".join(out))

# ---- Hebrew (UNGEGN, LAST-RESORT machine fallback) ----
# Hebrew omits most vowels; undotted text can't distinguish b/v, k/kh, p/f, so this is approximate
# (curated osm-en/GNS/GeoNames preferred). Final-form letters map like their base form.
HE = {
    "א":"ʼ","ב":"v","ג":"g","ד":"d","ה":"h","ו":"v","ז":"z","ח":"ẖ","ט":"t","י":"y",
    "כ":"kh","ך":"kh","ל":"l","מ":"m","ם":"m","נ":"n","ן":"n","ס":"s","ע":"ʻ","פ":"f","ף":"f",
    "צ":"ts","ץ":"ts","ק":"q","ר":"r","ש":"sh","ת":"t","׳":"","״":"",
}
def tr_he(s):
    return titlecase("".join(HE.get(ch, ch if script_of_char(ch) is None else "") for ch in s))

SYSTEMS = {
    "uk-2010": tr_uk, "bg-2009": tr_bg, "ru-bgn": tr_ru, "be-2007": tr_be,
    "sr-latn": lambda s: _simple(s, SR), "mk-nat": lambda s: _simple(s, MK),
}
COUNTRY_SYSTEM = {
    "UA": "uk-2010", "RU": "ru-bgn", "BY": "be-2007", "BG": "bg-2009",
    "RS": "sr-latn", "BA": "sr-latn", "ME": "sr-latn", "MK": "mk-nat",
}
DEFAULT_CYRILLIC = "ru-bgn"

def cyrillic_system_for(cc):
    return COUNTRY_SYSTEM.get(cc or "", DEFAULT_CYRILLIC)

# Script-based systems (script determines the standard, independent of country)
SCRIPT_SYSTEM = {
    "Greek": ("el-843", tr_el), "Georgian": ("ka-2002", tr_ka), "Armenian": ("hy-bgn", tr_hy),
    "Arabic": ("ar-bgn", tr_ar), "Hebrew": ("he-ungegn", tr_he),
}
# Scripts romanized deterministically by a documented standard (consistency for citations).
DETERMINISTIC = {"Cyrillic", "Greek", "Georgian", "Armenian"}
# Scripts where machine transliteration is lossy -> prefer curated OSM/gazetteer names first.
CURATED_FIRST = {"Arabic", "Hebrew"}
HANDLED = DETERMINISTIC | CURATED_FIRST

def _latin_ok(s):
    """True if s is a usable Latin label (non-empty, dominant script Latin) — guards against
    OSM name:en/name:fr tags that are themselves non-Latin (rare mis-tagging)."""
    return bool(s) and script_of_name(s) == "Latin"

def machine_translit(name, cc, sc):
    """The deterministic/machine romanization for a handled script (no curated cascade).
    Returns (romanized_name, name_source_code) or (None, None)."""
    if sc == "Cyrillic":
        sys_id = cyrillic_system_for(cc); return SYSTEMS[sys_id](name), f"translit-{sys_id}"
    if sc in SCRIPT_SYSTEM:
        code, fn = SCRIPT_SYSTEM[sc]; return fn(name), f"translit-{code}"
    return None, None

def romanize_name(name, cc, sc, name_en=None, name_fr=None):
    """Full cascade -> (romanized_name, name_source_code), or (None, None) if unhandled.
    Deterministic scripts: romanize uniformly by the standard. Arabic/Hebrew: curated-first
    (osm-en -> osm-fr[Arabic] -> [gns/geonames, later] -> machine translit)."""
    if sc in CURATED_FIRST:
        if _latin_ok(name_en): return name_en, "osm-en"
        if sc == "Arabic" and _latin_ok(name_fr): return name_fr, "osm-fr"
        return machine_translit(name, cc, sc)  # last resort (GNS/GeoNames slot in before this)
    return machine_translit(name, cc, sc)

# ---------------------------------------------------------------------------
def rows(con, layer):
    return con.execute(f"SELECT fid, name, country_iso, name_en, name_fr FROM {layer}").fetchall()

def _norm(x):
    """Casefold + strip combining diacritics — isolates structural agreement from the
    casing/diacritic/exonym noise in OSM name:en (a poor oracle)."""
    x = unicodedata.normalize("NFKD", x or "")
    x = "".join(c for c in x if not unicodedata.combining(c))
    return x.casefold().replace("’", "'").strip()

def validate(con):
    print("=== Validation: translit(name) vs OSM name:en (name:en is a NOISY oracle: it")
    print("    strips diacritics, uses exonyms, and title-cases; 'norm' = case/diacritic-insensitive) ===")
    per_sys = collections.defaultdict(lambda: [0, 0, 0, []])  # sys -> [raw, norm, total, samples]
    for layer in ("places", "admin_levels"):
        for fid, name, cc, name_en, name_fr in rows(con, layer):
            sc = script_of_name(name)
            if sc not in HANDLED or not name_en:
                continue
            # Always compare the *machine* transliteration (for Arabic/Hebrew the cascade would
            # just echo name:en) — this gauges the last-resort fallback's quality.
            got, code = machine_translit(name, cc, sc)
            rec = per_sys[code]
            rec[2] += 1
            if got == name_en: rec[0] += 1
            if _norm(got) == _norm(name_en): rec[1] += 1
            elif len(rec[3]) < 8:
                rec[3].append((name, got, name_en))
    for code in sorted(per_sys):
        raw, nrm, t, samp = per_sys[code]
        if not t:
            print(f"\n{code}: no name:en to compare"); continue
        print(f"\n{code}: raw {raw}/{t} ({100*raw/t:.1f}%) | structural (norm) {nrm}/{t} ({100*nrm/t:.1f}%)")
        for name, got, en in samp:
            print(f"    {name!r}  translit={got!r}  name:en={en!r}")

def apply(con):
    # name_source defaults to 'latin-osm' via ADD COLUMN DEFAULT (O(1), no rewrite of the
    # 622k Latin rows). Only the ~161k non-Latin rows are UPDATEd below.
    for layer in ("places", "admin_levels"):
        cols = [r[1] for r in con.execute(f"PRAGMA table_info({layer})")]
        if "name_source" not in cols:
            con.execute(f"ALTER TABLE {layer} ADD COLUMN name_source TEXT DEFAULT 'latin-osm'")
        if "name_native" not in cols:
            con.execute(f"ALTER TABLE {layer} ADD COLUMN name_native TEXT")
    counts = collections.Counter()
    for layer in ("places", "admin_levels"):
        updates, pending = [], []
        for fid, name, cc, name_en, name_fr in rows(con, layer):
            sc = script_of_name(name)
            if sc == "Latin":
                counts["latin-osm"] += 1  # handled by DEFAULT
            elif sc in HANDLED:
                got, code = romanize_name(name, cc, sc, name_en, name_fr)
                updates.append((name, code, got, fid)); counts[code] += 1  # (native, code, roman, fid)
            elif sc in ("EMPTY", "NONALPHA"):
                counts["(empty)"] += 1
            else:
                pending.append((fid,)); counts[f"(pending:{sc})"] += 1  # future scripts (Han/Kana/…)
        con.executemany(
            f"UPDATE {layer} SET name_native=?, name_source=?, name=? WHERE fid=?", updates)
        # scripts not handled yet: mark name_source NULL (pending)
        con.executemany(f"UPDATE {layer} SET name_source=NULL WHERE fid=?", pending)
    con.commit()
    print("Applied. name_source distribution:")
    for k, v in counts.most_common():
        print(f"  {k:24} {v}")

def check(con):
    """Assert the post-romanization guarantees (used by `make verify`). Exit 1 on any
    violation. Reports per-source counts and the residual unhandled (NULL) names."""
    bad_latin = null_src = empty_native = 0
    dist = collections.Counter()
    for layer in ("places", "admin_levels"):
        for name, src, native in con.execute(
                f"SELECT name, name_source, name_native FROM {layer}"):
            dist[src if src is not None else "(NULL)"] += 1
            if src is None:
                null_src += 1
                continue
            # every non-empty romanized name must be dominant-Latin (the 100%-Latin guarantee)
            if name and script_of_name(name) not in ("Latin", "NONALPHA"):
                bad_latin += 1
            if src.startswith("translit-") and not native:
                empty_native += 1
    print("name_source distribution:")
    for k, v in dist.most_common():
        print(f"  {k:24} {v}")
    print(f"non-Latin romanized names (must be 0): {bad_latin}")
    print(f"translit-* rows w/o name_native (must be 0): {empty_native}")
    print(f"unhandled names (name_source NULL): {null_src}")
    fail = bad_latin or empty_native
    print("CHECK:", "FAIL" if fail else "OK")
    return 1 if fail else 0

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--validate", action="store_true")
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--check", action="store_true", help="assert post-romanization guarantees")
    ap.add_argument("--gpkg", default=G, help="path to the GeoPackage")
    a = ap.parse_args()
    con = sqlite3.connect(a.gpkg)
    if a.check: sys.exit(check(con))
    if a.validate: validate(con)
    elif a.apply: apply(con)
    else: ap.print_help()
    con.close()

if __name__ == "__main__":
    main()
