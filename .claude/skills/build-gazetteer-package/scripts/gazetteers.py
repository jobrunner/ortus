#!/usr/bin/env python3
"""External-gazetteer romanization sources for the Arabic/Hebrew cascade (see
docs/reference/romanization.md). Two authoritative sources, tried in this order by
`romanize_gazetteers.py`, between the OSM tags and the machine last-resort:

  gns-bgn   NGA GEOnet Names Server (BGN/PCGN romanization) -- public domain (US Gov).
            Queried live from the GNS ArcGIS REST service; the roman name is the approved
            (nt='N') Latin-script variant of a feature, matched to the OSM native name.
  geonames  GeoNames per-country bulk dump (CC BY 4.0). The `asciiname`/Latin alternate is
            matched to the OSM native name via the native-script alternatenames.

Both are matched by (normalized native name + same country + nearest coordinate). Downloads
are cached under temp/gazetteers/ so a re-run is offline and idempotent.
"""
import io, json, os, sys, time, unicodedata, urllib.parse, urllib.request, zipfile, collections

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from romanize import script_of_name  # shared Unicode-script buckets

CACHE = "temp/gazetteers"
GNS_URL = "https://geonames.nga.mil/geon-ags/rest/services/RESEARCH/GIS_OUTPUT/MapServer/0/query"
GEONAMES_URL = "https://download.geonames.org/export/dump/{cc}.zip"
PAGE = 3000  # GNS maxRecordCount

# ISO 3166-1 alpha-2 -> GENC alpha-3 (== ISO alpha-3 for every country we touch). GNS keys
# its `cc_ft` on GENC-3; GeoNames keys its dump filename on ISO-2.
ISO2_GENC3 = {
    "IQ":"IRQ","SA":"SAU","OM":"OMN","JO":"JOR","MA":"MAR","EG":"EGY","IR":"IRN","LY":"LBY",
    "PS":"PSE","SY":"SYR","BH":"BHR","DZ":"DZA","AE":"ARE","TN":"TUN","TR":"TUR","YE":"YEM",
    "IL":"ISR","QA":"QAT","KW":"KWT",
}

# ---- native-script normalization (maximise native<->native match recall) ----
_AR_DIAC = {chr(c) for c in range(0x064B, 0x0653)} | {"ـ", "ٰ", "ٓ", "ٔ", "ٕ"}
_AR_FOLD = str.maketrans({"أ":"ا","إ":"ا","آ":"ا","ٱ":"ا","ى":"ي","ئ":"ي","ؤ":"و","ة":"ه","ک":"ك","ی":"ي","ۆ":"و","ێ":"ي","ە":"ه","ھ":"ه"})
def norm_native(s):
    """Fold an Arabic/Hebrew name to a match key: drop harakat/tatweel, unify alef/ya/ta-marbuta
    and the Persian/Kurdish letter variants, casefold, collapse whitespace. Non-destructive of
    the stored value — used only for lookup keying."""
    if not s: return ""
    s = unicodedata.normalize("NFKC", s)
    s = "".join(ch for ch in s if ch not in _AR_DIAC)
    s = s.translate(_AR_FOLD)
    return " ".join(s.split()).strip().casefold()

def _latin(s):
    return bool(s) and script_of_name(s) == "Latin"

def _dist2(a, b):
    return (a[0]-b[0])**2 + (a[1]-b[1])**2

# ---------------------------------------------------------------------------
def _fetch_json(url):
    with urllib.request.urlopen(url, timeout=120) as r:
        return json.load(r)

def fetch_gns(cc_iso2, kinds=("P", "A"), cache=CACHE, log=print):
    """Download all GNS name rows for a country (feature classes in `kinds`), paginated, into
    a cached jsonl. Returns the parsed feature-attribute dicts."""
    genc3 = ISO2_GENC3.get(cc_iso2)
    if not genc3:
        raise KeyError(f"no GENC-3 mapping for {cc_iso2}")
    os.makedirs(cache, exist_ok=True)
    path = os.path.join(cache, f"gns_{cc_iso2}.jsonl")
    if os.path.exists(path):
        with open(path) as f:
            return [json.loads(l) for l in f]
    fc_clause = " OR ".join(f"fc='{k}'" for k in kinds)
    where = f"cc_ft='{genc3}' AND ({fc_clause})"
    fields = "ufi,full_name,script_cd,nt,name_rank,fc,lat_dd,long_dd"
    out, offset = [], 0
    while True:
        q = urllib.parse.urlencode({
            "where": where, "outFields": fields, "orderByFields": "ufi",
            "resultOffset": offset, "resultRecordCount": PAGE, "returnGeometry": "false", "f": "json"})
        d = _fetch_json(f"{GNS_URL}?{q}")
        feats = [ft["attributes"] for ft in d.get("features", [])]
        out.extend(feats)
        log(f"  gns {cc_iso2}: +{len(feats)} (total {len(out)})")
        if not d.get("exceededTransferLimit") or not feats:
            break
        offset += len(feats)
        time.sleep(0.2)
    with open(path, "w") as f:
        for a in out:
            f.write(json.dumps(a, ensure_ascii=False) + "\n")
    return out

def fetch_geonames(cc_iso2, cache=CACHE, log=print):
    """Download + extract the GeoNames per-country dump into a cached .txt. Returns its path."""
    os.makedirs(cache, exist_ok=True)
    path = os.path.join(cache, f"{cc_iso2}.txt")
    if os.path.exists(path):
        return path
    url = GEONAMES_URL.format(cc=cc_iso2)
    log(f"  geonames {cc_iso2}: downloading {url}")
    with urllib.request.urlopen(url, timeout=180) as r:
        data = r.read()
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        with z.open(f"{cc_iso2}.txt") as src, open(path, "wb") as dst:
            dst.write(src.read())
    return path

# ---------------------------------------------------------------------------
class Index:
    """Per-country native-name -> [(roman, lat, lon)] lookup, over one or both gazetteers.
    lookup() returns the roman form of the nearest same-name feature to (lat, lon)."""
    def __init__(self, source):
        self.source = source            # 'gns-bgn' | 'geonames'
        self.by_native = collections.defaultdict(list)  # norm_native -> [(roman, lat, lon)]

    def add(self, native, roman, lat, lon):
        if native and roman and lat is not None and lon is not None:
            self.by_native[norm_native(native)].append((roman, lat, lon))

    def lookup(self, native, lat, lon, max_deg=0.25):
        cands = self.by_native.get(norm_native(native))
        if not cands:
            return None
        best, bestd = None, None
        for roman, la, lo in cands:
            d = _dist2((lat, lon), (la, lo))
            if bestd is None or d < bestd:
                best, bestd = roman, d
        # guard against a same-name feature on the other side of the country
        if bestd is not None and bestd <= max_deg * max_deg:
            return best
        return None

def build_gns_index(features):
    """From GNS name rows -> Index. Per feature (ufi): native = any non-Latin full_name; roman
    = the approved (nt='N', best name_rank) Latin variant, else any Latin variant."""
    by_ufi = collections.defaultdict(list)
    for a in features:
        by_ufi[a["ufi"]].append(a)
    idx = Index("gns-bgn")
    for ufi, rows in by_ufi.items():
        lat = rows[0].get("lat_dd"); lon = rows[0].get("long_dd")
        natives = [r["full_name"] for r in rows
                   if r.get("full_name") and script_of_name(r["full_name"]) == "Arabic"]
        # roman preference: nt='N' with the smallest name_rank, then any Latin full_name
        romans = [r for r in rows if r.get("full_name") and _latin(r["full_name"])]
        approved = [r for r in romans if r.get("nt") == "N"]
        approved.sort(key=lambda r: (r.get("name_rank") or 9999))
        roman = (approved[0]["full_name"] if approved else (romans[0]["full_name"] if romans else None))
        if not roman:
            continue
        for nat in natives:
            idx.add(nat, roman, lat, lon)
    return idx

def build_geonames_population_index(path):
    """From a GeoNames dump -> Index mapping native (non-Latin) name -> the nearest feature's
    `population` (column 14) as its payload string. Features with unknown population (0) are
    skipped. Used by enrich_places.py to backfill the sparse MENA village population tail."""
    idx = Index("geonames-pop")
    with open(path, encoding="utf-8") as f:
        for line in f:
            c = line.rstrip("\n").split("\t")
            if len(c) < 15:
                continue
            # column 6 = feature_class; restrict to 'P' (populated place) so we never take the
            # population of an enclosing admin area (class 'A') for a same-named settlement
            if c[6] != "P":
                continue
            name, alt = c[1], c[3]
            try:
                lat, lon, pop = float(c[4]), float(c[5]), int(c[14])
            except ValueError:
                continue
            if pop <= 0:
                continue
            natives = [n for n in ([name] + alt.split(",")) if n and script_of_name(n) != "Latin"]
            for nat in natives:
                idx.add(nat, str(pop), lat, lon)
    return idx


def build_geonames_index(path):
    """From a GeoNames dump -> Index. native names come from `alternatenames` (non-Latin ones);
    roman = `asciiname` (or `name` if Latin)."""
    idx = Index("geonames")
    with open(path, encoding="utf-8") as f:
        for line in f:
            c = line.rstrip("\n").split("\t")
            if len(c) < 15:
                continue
            name, asciiname, alt = c[1], c[2], c[3]
            try:
                lat, lon = float(c[4]), float(c[5])
            except ValueError:
                continue
            # prefer `name` when Latin (it keeps diacritics — better for citation) over the
            # diacritic-stripped asciiname
            roman = name if _latin(name) else (asciiname if _latin(asciiname) else None)
            if not roman:
                continue
            natives = [name] if not _latin(name) else []
            natives += [a for a in alt.split(",") if a and script_of_name(a) == "Arabic"]
            for nat in natives:
                idx.add(nat, roman, lat, lon)
    return idx
