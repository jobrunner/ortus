package http

import (
	"net/http"
)

// frontendHTML is the embedded HTML for the coordinate query frontend.
// Mobile-first, responsive design with pure CSS.
const frontendHTML = `<!DOCTYPE html>
<html lang="de">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Ortus - Koordinatenabfrage</title>
    <style>
        :root {
            --primary: #2563eb;
            --primary-dark: #1d4ed8;
            --success: #16a34a;
            --error: #dc2626;
            --warning: #d97706;
            --bg: #f8fafc;
            --card: #ffffff;
            --text: #1e293b;
            --text-muted: #64748b;
            --border: #e2e8f0;
            --radius: 8px;
            --shadow: 0 1px 3px rgba(0,0,0,0.1);
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: var(--bg);
            color: var(--text);
            line-height: 1.5;
            min-height: 100vh;
        }

        .container {
            max-width: 800px;
            margin: 0 auto;
            padding: 1rem;
        }

        header {
            text-align: center;
            padding: 1.5rem 0;
            border-bottom: 1px solid var(--border);
            margin-bottom: 1.5rem;
        }

        header h1 {
            font-size: 1.5rem;
            font-weight: 600;
            color: var(--primary);
        }

        header p {
            color: var(--text-muted);
            font-size: 0.875rem;
            margin-top: 0.25rem;
        }

        .card {
            background: var(--card);
            border-radius: var(--radius);
            box-shadow: var(--shadow);
            padding: 1.25rem;
            margin-bottom: 1rem;
        }

        .card-title {
            font-size: 0.875rem;
            font-weight: 600;
            color: var(--text-muted);
            text-transform: uppercase;
            letter-spacing: 0.05em;
            margin-bottom: 1rem;
        }

        .form-group {
            margin-bottom: 1rem;
        }

        label {
            display: block;
            font-size: 0.875rem;
            font-weight: 500;
            margin-bottom: 0.375rem;
            color: var(--text);
        }

        input, select {
            width: 100%;
            padding: 0.625rem 0.75rem;
            font-size: 1rem;
            border: 1px solid var(--border);
            border-radius: var(--radius);
            background: var(--card);
            color: var(--text);
            transition: border-color 0.15s, box-shadow 0.15s;
        }

        input:focus, select:focus {
            outline: none;
            border-color: var(--primary);
            box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.1);
        }

        input::placeholder {
            color: var(--text-muted);
        }

        .coord-grid {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 0.75rem;
        }

        .btn {
            display: inline-flex;
            align-items: center;
            justify-content: center;
            width: 100%;
            min-height: 44px;
            padding: 0.75rem 1rem;
            font-size: 1rem;
            font-weight: 500;
            color: white;
            background: var(--primary);
            border: none;
            border-radius: var(--radius);
            cursor: pointer;
            transition: background-color 0.15s;
        }

        .btn:hover {
            background: var(--primary-dark);
        }

        /* Visible keyboard-focus ring for buttons and the (focusable) source headers. */
        .btn:focus-visible,
        .source-header:focus-visible {
            outline: 2px solid var(--primary);
            outline-offset: 2px;
        }

        .btn:disabled {
            background: var(--text-muted);
            cursor: not-allowed;
        }

        .btn-secondary {
            background: var(--card);
            color: var(--text);
            border: 1px solid var(--border);
        }

        .btn-secondary:hover {
            background: var(--bg);
        }

        .btn-row {
            display: grid;
            grid-template-columns: 1fr auto;
            gap: 0.5rem;
        }

        .loading {
            display: none;
            text-align: center;
            padding: 2rem;
            color: var(--text-muted);
        }

        .loading.active {
            display: block;
        }

        .spinner {
            width: 24px;
            height: 24px;
            border: 2px solid var(--border);
            border-top-color: var(--primary);
            border-radius: 50%;
            animation: spin 0.8s linear infinite;
            margin: 0 auto 0.5rem;
        }

        @keyframes spin {
            to { transform: rotate(360deg); }
        }

        .error {
            background: #fef2f2;
            border: 1px solid #fecaca;
            color: var(--error);
            padding: 0.75rem 1rem;
            border-radius: var(--radius);
            font-size: 0.875rem;
            margin-bottom: 1rem;
            display: none;
        }

        .error.active {
            display: block;
        }

        #results {
            display: none;
        }

        #results.active {
            display: block;
        }

        .result-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            flex-wrap: wrap;
            gap: 0.5rem;
            margin-bottom: 1rem;
            padding-bottom: 0.75rem;
            border-bottom: 1px solid var(--border);
        }

        .result-coord {
            font-family: 'SF Mono', Monaco, monospace;
            font-size: 0.8125rem;
            background: var(--bg);
            padding: 0.25rem 0.5rem;
            border-radius: 4px;
        }

        .result-stats {
            font-size: 0.8125rem;
            color: var(--text-muted);
        }

        .source-card {
            border: 1px solid var(--border);
            border-radius: var(--radius);
            margin-bottom: 0.75rem;
            overflow: hidden;
        }

        .source-header {
            display: flex;
            align-items: flex-start;
            gap: 0.5rem;
            min-height: 44px;
            padding: 0.75rem 1rem;
            background: var(--bg);
            cursor: pointer;
            user-select: none;
        }

        .source-header:hover {
            background: #f1f5f9;
        }

        /* Title + meta take the row and may shrink/wrap; the chevron stays pinned
           top-right so a long source name never fights it. */
        .source-main {
            flex: 1;
            min-width: 0;
        }

        .source-name {
            display: block;
            font-weight: 500;
            font-size: 0.9375rem;
            line-height: 1.3;
        }

        .source-meta {
            display: flex;
            flex-wrap: wrap;
            align-items: center;
            gap: 0.5rem;
            margin-top: 0.4rem;
            font-size: 0.75rem;
            color: var(--text-muted);
        }

        .source-time {
            font-variant-numeric: tabular-nums;
        }

        .badge {
            display: inline-flex;
            align-items: center;
            padding: 0.125rem 0.5rem;
            font-size: 0.75rem;
            font-weight: 500;
            border-radius: 9999px;
            background: #dbeafe;
            color: var(--primary);
        }

        .badge-success {
            background: #dcfce7;
            color: var(--success);
        }

        .source-content {
            display: none;
            padding: 1rem;
            border-top: 1px solid var(--border);
        }

        .source-card.expanded .source-content {
            display: block;
        }

        .feature {
            background: var(--bg);
            border-radius: var(--radius);
            padding: 0.75rem;
            margin-bottom: 0.5rem;
        }

        .feature:last-child {
            margin-bottom: 0;
        }

        .feature-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 0.5rem;
        }

        .feature-layer {
            font-size: 0.8125rem;
            font-weight: 500;
        }

        .feature-id {
            font-size: 0.75rem;
            color: var(--text-muted);
            font-family: monospace;
        }

        .properties-table {
            width: 100%;
            font-size: 0.8125rem;
            border-collapse: collapse;
        }

        .properties-table th,
        .properties-table td {
            text-align: left;
            padding: 0.375rem 0.5rem;
            border-bottom: 1px solid var(--border);
        }

        .properties-table th {
            font-weight: 500;
            color: var(--text-muted);
            width: 40%;
        }

        .properties-table tr:last-child th,
        .properties-table tr:last-child td {
            border-bottom: none;
        }

        /* Colour-valued properties (e.g. a #RRGGBB class colour) get a swatch. */
        .value-swatch {
            display: inline-block;
            width: 0.85em;
            height: 0.85em;
            border-radius: 3px;
            border: 1px solid rgba(0,0,0,0.15);
            vertical-align: -1px;
            margin-right: 0.4em;
        }

        .value-color {
            font-family: monospace;
        }

        .geometry-preview {
            margin-top: 0.5rem;
            padding: 0.5rem;
            background: #f8fafc;
            border: 1px solid var(--border);
            border-radius: 4px;
            font-family: monospace;
            font-size: 0.75rem;
            color: var(--text-muted);
            word-break: break-all;
            max-height: 80px;
            overflow-y: auto;
        }

        .license-info {
            margin-top: 0.75rem;
            padding-top: 0.75rem;
            border-top: 1px solid var(--border);
            font-size: 0.75rem;
            color: var(--text-muted);
        }

        .license-info a {
            color: var(--primary);
            text-decoration: none;
        }

        .license-info a:hover {
            text-decoration: underline;
        }

        .no-results {
            text-align: center;
            padding: 2rem;
            color: var(--text-muted);
        }

        .toggle-icon {
            flex: none;
            margin-top: 2px;
            color: var(--text-muted);
            transition: transform 0.2s;
        }

        .source-card.expanded .toggle-icon {
            transform: rotate(180deg);
        }

        /* Gazetteer (location context) block */
        .gazetteer-block {
            border: 1px solid var(--border);
            border-left: 3px solid var(--primary);
            border-radius: var(--radius);
            background: var(--bg);
            padding: 0.875rem 1rem;
            margin-bottom: 1rem;
        }

        .gazetteer-title {
            font-size: 0.9375rem;
            font-weight: 600;
            margin-bottom: 0.75rem;
        }

        .gaz-section {
            padding-top: 0.75rem;
            margin-top: 0.75rem;
            border-top: 1px solid var(--border);
        }

        .gaz-section:first-of-type {
            padding-top: 0;
            margin-top: 0;
            border-top: none;
        }

        .gaz-label {
            font-size: 0.75rem;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            color: var(--text-muted);
            margin-bottom: 0.5rem;
        }

        .admin-list, .source-explain-list {
            list-style: none;
        }

        .admin-item {
            padding: 0.5rem 0;
            border-bottom: 1px solid var(--border);
        }

        .admin-item:last-child {
            border-bottom: none;
        }

        .admin-line {
            display: flex;
            align-items: baseline;
            flex-wrap: wrap;
            gap: 0.375rem;
        }

        .admin-name {
            font-weight: 500;
        }

        .admin-native {
            color: var(--text-muted);
            font-size: 0.875rem;
        }

        .admin-level {
            margin-left: auto;
            font-size: 0.75rem;
            color: var(--text-muted);
            font-family: monospace;
        }

        .admin-tier {
            font-size: 0.8125rem;
            color: var(--primary);
            margin-top: 0.125rem;
        }

        .admin-desc {
            font-size: 0.8125rem;
            color: var(--text-muted);
            margin-top: 0.125rem;
        }

        .admin-src, .gaz-bearing-meta {
            font-size: 0.75rem;
            color: var(--text-muted);
            margin-top: 0.125rem;
        }

        .admin-src code, .source-explain-list code {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 4px;
            padding: 0 0.25rem;
            font-size: 0.75rem;
        }

        .gaz-bearing {
            font-weight: 500;
        }

        .source-explain-list li {
            font-size: 0.8125rem;
            padding: 0.25rem 0;
        }

        .src-standard {
            color: var(--text-muted);
        }

        .gaz-license {
            font-size: 0.8125rem;
            color: var(--text-muted);
        }

        .gaz-license a {
            color: var(--primary);
            text-decoration: none;
        }

        .gaz-license a:hover {
            text-decoration: underline;
        }

        .gaz-attribution {
            margin-top: 0.25rem;
        }

        footer {
            text-align: center;
            padding: 1.5rem 0;
            color: var(--text-muted);
            font-size: 0.75rem;
            border-top: 1px solid var(--border);
            margin-top: 2rem;
        }

        footer a {
            color: var(--primary);
            text-decoration: none;
        }

        footer a:hover {
            text-decoration: underline;
        }

        /* Tablet and up */
        @media (min-width: 640px) {
            .container {
                padding: 2rem;
            }

            header {
                padding: 2rem 0;
            }

            header h1 {
                font-size: 1.75rem;
            }

            .card {
                padding: 1.5rem;
            }

            .btn-row {
                grid-template-columns: 1fr auto auto;
            }
        }

        /* Desktop */
        @media (min-width: 1024px) {
            .container {
                padding: 2rem 1rem;
            }
        }

        /* Respect users who ask for less motion (e.g. vestibular disorders). */
        @media (prefers-reduced-motion: reduce) {
            *, *::before, *::after {
                animation-duration: 0.01ms !important;
                animation-iteration-count: 1 !important;
                transition-duration: 0.01ms !important;
            }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Ortus</h1>
            <p>Point-in-Polygon Abfrage über Datenquellen</p>
        </header>

        <div class="card">
            <h2 class="card-title">Koordinaten eingeben</h2>
            <form id="queryForm">
                <div class="form-group">
                    <label for="srid">Koordinatensystem</label>
                    <select id="srid" name="srid">
                        <option value="4326">WGS 84 (EPSG:4326) - GPS</option>
                        <option value="3857">Web Mercator (EPSG:3857)</option>
                        <option value="25832">ETRS89 / UTM Zone 32N (EPSG:25832)</option>
                        <option value="25833">ETRS89 / UTM Zone 33N (EPSG:25833)</option>
                        <option value="31466">DHDN / Gauß-Krüger Zone 2 (EPSG:31466)</option>
                        <option value="31467">DHDN / Gauß-Krüger Zone 3 (EPSG:31467)</option>
                    </select>
                </div>

                <div class="coord-grid" id="coordGrid">
                    <div class="form-group" id="groupY">
                        <label for="coordY" id="labelY">Breitengrad (Lat)</label>
                        <input type="text" id="coordY" name="y" placeholder="z.B. 52.52" inputmode="decimal" required>
                    </div>
                    <div class="form-group" id="groupX">
                        <label for="coordX" id="labelX">Längengrad (Lon)</label>
                        <input type="text" id="coordX" name="x" placeholder="z.B. 13.405" inputmode="decimal" required>
                    </div>
                </div>

                <div class="btn-row">
                    <button type="submit" class="btn" id="submitBtn">Abfragen</button>
                    <button type="button" class="btn btn-secondary" id="locationBtn" title="Aktuellen Standort verwenden" aria-label="Aktuellen Standort verwenden">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true" focusable="false">
                            <circle cx="12" cy="12" r="3"/>
                            <path d="M12 2v4m0 12v4M2 12h4m12 0h4"/>
                        </svg>
                    </button>
                    <button type="button" class="btn btn-secondary" id="clearBtn">Leeren</button>
                </div>
            </form>
        </div>

        <div class="error" id="error" role="alert"></div>

        <div class="loading" id="loading" role="status" aria-live="polite">
            <div class="spinner"></div>
            <p>Abfrage wird ausgeführt...</p>
        </div>

        <div id="results">
            <div class="card">
                <h2 class="card-title">Ergebnisse</h2>
                <div class="result-header" role="status" aria-live="polite">
                    <span class="result-coord" id="resultCoord"></span>
                    <span class="result-stats" id="resultStats"></span>
                </div>
                <div id="resultContent"></div>
            </div>
        </div>

        <footer>
            <a href="/docs">API Dokumentation</a> &middot;
            <a href="/openapi.json">OpenAPI Spec</a> &middot;
            <a href="/health">Health Status</a>
        </footer>
    </div>

    <script>
        (function() {
            const form = document.getElementById('queryForm');
            const sridSelect = document.getElementById('srid');
            const coordX = document.getElementById('coordX');
            const coordY = document.getElementById('coordY');
            const groupX = document.getElementById('groupX');
            const groupY = document.getElementById('groupY');
            const coordGrid = document.getElementById('coordGrid');
            const labelX = document.getElementById('labelX');
            const labelY = document.getElementById('labelY');
            const submitBtn = document.getElementById('submitBtn');
            const locationBtn = document.getElementById('locationBtn');
            const clearBtn = document.getElementById('clearBtn');
            const loading = document.getElementById('loading');
            const error = document.getElementById('error');
            const results = document.getElementById('results');
            const resultCoord = document.getElementById('resultCoord');
            const resultStats = document.getElementById('resultStats');
            const resultContent = document.getElementById('resultContent');

            // SRID-specific labels and placeholders
            const sridConfig = {
                '4326': {
                    xLabel: 'Längengrad (Lon)', yLabel: 'Breitengrad (Lat)',
                    xPlaceholder: 'z.B. 13.405', yPlaceholder: 'z.B. 52.52'
                },
                '3857': {
                    xLabel: 'X (Meter)', yLabel: 'Y (Meter)',
                    xPlaceholder: 'z.B. 1492273', yPlaceholder: 'z.B. 6894026'
                },
                '25832': {
                    xLabel: 'Rechtswert (E)', yLabel: 'Hochwert (N)',
                    xPlaceholder: 'z.B. 389524', yPlaceholder: 'z.B. 5820270'
                },
                '25833': {
                    xLabel: 'Rechtswert (E)', yLabel: 'Hochwert (N)',
                    xPlaceholder: 'z.B. 389524', yPlaceholder: 'z.B. 5820270'
                },
                '31466': {
                    xLabel: 'Rechtswert', yLabel: 'Hochwert',
                    xPlaceholder: 'z.B. 2597000', yPlaceholder: 'z.B. 5735000'
                },
                '31467': {
                    xLabel: 'Rechtswert', yLabel: 'Hochwert',
                    xPlaceholder: 'z.B. 3597000', yPlaceholder: 'z.B. 5735000'
                }
            };

            // WGS84 uses the classic navigation order (latitude first, longitude
            // second); projected systems keep the usual Rechtswert (X) before
            // Hochwert (Y). Only the visual field order changes — the id/name→query
            // mapping (coordX→lon/x, coordY→lat/y) stays the same.
            function applyFieldOrder(srid) {
                if (srid === '4326') {
                    coordGrid.insertBefore(groupY, groupX);
                } else {
                    coordGrid.insertBefore(groupX, groupY);
                }
            }
            applyFieldOrder(sridSelect.value);

            // Update labels when SRID changes
            sridSelect.addEventListener('change', function() {
                const config = sridConfig[this.value] || sridConfig['4326'];
                labelX.textContent = config.xLabel;
                labelY.textContent = config.yLabel;
                coordX.placeholder = config.xPlaceholder;
                coordY.placeholder = config.yPlaceholder;
                applyFieldOrder(this.value);

                // Clear values when switching coordinate systems
                coordX.value = '';
                coordY.value = '';
            });

            // Smart coordinate paste: pasting a full pair like "35.016132, 32.670024"
            // (or ";"-separated, or with German decimal commas like "35,016132;32,670024")
            // into EITHER field splits it across both — first part into the visually
            // first field, second into the second. A single value pastes normally.
            function parseCoordinatePair(text) {
                const t = (text || '').trim();
                if (!t) return null;
                const hasSemi = t.indexOf(';') >= 0;
                const hasComma = t.indexOf(',') >= 0;
                const hasDot = t.indexOf('.') >= 0;
                const hasSpace = /\s/.test(t);
                let parts, commaIsDecimal;
                if (hasSemi) {
                    parts = t.split(';'); commaIsDecimal = true;          // "35,01;32,67" → comma is decimal
                } else if (hasComma && hasDot) {
                    parts = t.split(','); commaIsDecimal = false;         // "35.01, 32.67" → dot decimal, comma separates
                } else if (hasComma && hasSpace) {
                    parts = t.split(/\s+/); commaIsDecimal = true;        // "35,01 32,67" → space separates, comma is decimal
                } else if (!hasComma && hasSpace) {
                    parts = t.split(/\s+/); commaIsDecimal = false;       // "35.01 32.67" or "35 32"
                } else {
                    return null;                                          // single token (incl. lone "35,016132") → normal paste
                }
                if (!parts || parts.length < 2) return null;
                const a = normNum(parts[0], commaIsDecimal);
                const b = normNum(parts[1], commaIsDecimal);             // extra parts (>2) are ignored
                if (a === null || b === null) return null;
                return [a, b];
            }
            function normNum(s, commaIsDecimal) {
                s = (s || '').trim();
                if (commaIsDecimal) s = s.replace(',', '.');
                if (!/^[+-]?(\d+\.?\d*|\.\d+)$/.test(s)) return null;
                return s;
            }
            function handleCoordinatePaste(e) {
                const clip = e.clipboardData || window.clipboardData;
                if (!clip) return;
                // 'text/plain' is the standard type; 'text' is a legacy fallback (older IE/Edge).
                const pasted = clip.getData('text/plain') || clip.getData('text');
                const pair = parseCoordinatePair(pasted);
                if (!pair) return;                                        // single value → let the browser paste normally
                e.preventDefault();
                // Fill by visual order: for WGS84 the first field is lat (coordY),
                // otherwise the first field is X (coordX).
                const firstIsY = (sridSelect.value === '4326');
                (firstIsY ? coordY : coordX).value = pair[0];
                (firstIsY ? coordX : coordY).value = pair[1];
            }
            coordX.addEventListener('paste', handleCoordinatePaste);
            coordY.addEventListener('paste', handleCoordinatePaste);

            // Geolocation
            locationBtn.addEventListener('click', function() {
                if (!navigator.geolocation) {
                    showError('Geolokalisierung wird von Ihrem Browser nicht unterstützt.');
                    return;
                }

                locationBtn.disabled = true;
                navigator.geolocation.getCurrentPosition(
                    function(position) {
                        sridSelect.value = '4326';
                        sridSelect.dispatchEvent(new Event('change'));
                        coordX.value = position.coords.longitude.toFixed(6);
                        coordY.value = position.coords.latitude.toFixed(6);
                        locationBtn.disabled = false;
                    },
                    function(err) {
                        showError('Standort konnte nicht ermittelt werden: ' + err.message);
                        locationBtn.disabled = false;
                    },
                    { enableHighAccuracy: true, timeout: 10000 }
                );
            });

            // Clear form
            clearBtn.addEventListener('click', function() {
                coordX.value = '';
                coordY.value = '';
                hideError();
                results.classList.remove('active');
            });

            // Form submit
            form.addEventListener('submit', async function(e) {
                e.preventDefault();
                hideError();

                const srid = sridSelect.value;
                const x = parseFloat(coordX.value.replace(',', '.'));
                const y = parseFloat(coordY.value.replace(',', '.'));

                if (isNaN(x) || isNaN(y)) {
                    showError('Bitte geben Sie gültige Koordinaten ein.');
                    return;
                }

                // Build query URL with proper URL encoding
                let url = '/api/v1/query?srid=' + encodeURIComponent(srid);
                if (srid === '4326') {
                    url += '&lon=' + encodeURIComponent(x) + '&lat=' + encodeURIComponent(y);
                } else {
                    url += '&x=' + encodeURIComponent(x) + '&y=' + encodeURIComponent(y);
                }

                submitBtn.disabled = true;
                loading.classList.add('active');
                results.classList.remove('active');

                try {
                    const response = await fetch(url);

                    if (!response.ok) {
                        let errorMessage = 'Abfrage fehlgeschlagen';
                        try {
                            const errorData = await response.json();
                            errorMessage = errorData.error || errorData.message || errorMessage;
                        } catch (parseErr) {
                            // Response could not be parsed as JSON
                        }
                        throw new Error(errorMessage);
                    }

                    let data;
                    try {
                        data = await response.json();
                    } catch (parseErr) {
                        throw new Error('Die Serverantwort konnte nicht verarbeitet werden.');
                    }

                    displayResults(data, srid);
                } catch (err) {
                    showError(err.message);
                } finally {
                    submitBtn.disabled = false;
                    loading.classList.remove('active');
                }
            });

            function showError(message) {
                error.textContent = message;
                error.classList.add('active');
            }

            function hideError() {
                error.classList.remove('active');
            }

            function displayResults(data, srid) {
                // Header info
                const coord = data.coordinate;
                if (srid === '4326') {
                    resultCoord.textContent = 'Lon: ' + coord.x.toFixed(6) + ', Lat: ' + coord.y.toFixed(6);
                } else {
                    resultCoord.textContent = 'X: ' + coord.x.toFixed(2) + ', Y: ' + coord.y.toFixed(2) + ' (EPSG:' + coord.srid + ')';
                }

                resultStats.textContent = data.total_features + ' Feature(s) in ' + data.processing_time_ms + 'ms';

                let html = '';

                // Location context (gazetteer): admin hierarchy with level meaning,
                // bearing, name-source explanations and dataset attribution. Present
                // for WGS84 when the gazetteer feature is enabled — but only rendered
                // when it actually has location content (a point with no admin
                // coverage and no anchor would otherwise be an empty box; sources are
                // empty and the dataset license alone is not location context).
                if (hasGazetteerContent(data.gazetteer)) {
                    html += renderGazetteer(data.gazetteer);
                }

                // Point-in-polygon results
                if (!data.results || data.results.length === 0) {
                    html += '<div class="no-results">Keine Features an dieser Position gefunden.</div>';
                } else {
                    data.results.forEach(function(pkg, idx) {
                        html += renderSource(pkg, idx === 0);
                    });
                }
                resultContent.innerHTML = html;

                // Expand/collapse — keyboard-accessible (the header is role="button").
                document.querySelectorAll('.source-header').forEach(function(header) {
                    function toggle() {
                        const isExpanded = header.parentElement.classList.toggle('expanded');
                        header.setAttribute('aria-expanded', isExpanded ? 'true' : 'false');
                    }
                    header.addEventListener('click', toggle);
                    header.addEventListener('keydown', function(e) {
                        if (e.key === 'Enter' || e.key === ' ' || e.key === 'Spacebar') {
                            e.preventDefault();
                            toggle();
                        }
                    });
                });

                results.classList.add('active');
            }

            function renderSource(pkg, expanded) {
                let html = '<div class="source-card' + (expanded ? ' expanded' : '') + '">';
                html += '<div class="source-header" role="button" tabindex="0" aria-expanded="' + (expanded ? 'true' : 'false') + '">';
                html += '<div class="source-main">';
                html += '<span class="source-name">' + escapeHtml(pkg.source_name || pkg.source_id) + '</span>';
                html += '<div class="source-meta">';
                html += '<span class="badge">' + (pkg.feature_count === 1 ? '1 Feature' : pkg.feature_count + ' Features') + '</span>';
                html += '<span class="source-time">' + pkg.query_time_ms + ' ms</span>';
                html += '</div>'; // .source-meta
                html += '</div>'; // .source-main
                html += '<svg class="toggle-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true" focusable="false"><path d="M6 9l6 6 6-6"/></svg>';
                html += '</div>'; // .source-header

                html += '<div class="source-content">';

                if (pkg.features && pkg.features.length > 0) {
                    pkg.features.forEach(function(feature) {
                        html += renderFeature(feature);
                    });
                }

                if (pkg.license) {
                    html += '<div class="license-info">';
                    html += '<strong>Lizenz:</strong> ';
                    if (pkg.license.url) {
                        html += '<a href="' + escapeHtml(pkg.license.url) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(pkg.license.name || 'Link') + '</a>';
                    } else {
                        html += escapeHtml(pkg.license.name || '-');
                    }
                    if (pkg.license.attribution) {
                        html += ' &middot; ' + escapeHtml(pkg.license.attribution);
                    }
                    html += '</div>';
                }

                html += '</div></div>';
                return html;
            }

            function renderFeature(feature) {
                let html = '<div class="feature">';
                html += '<div class="feature-header">';
                html += '<span class="feature-layer">' + escapeHtml(feature.layer || '-') + '</span>';
                html += '<span class="feature-id">ID: ' + escapeHtml(feature.id || '-') + '</span>';
                html += '</div>';

                if (feature.properties && Object.keys(feature.properties).length > 0) {
                    html += '<table class="properties-table">';
                    for (const [key, value] of Object.entries(feature.properties)) {
                        html += '<tr><th>' + escapeHtml(key) + '</th><td>' + formatValue(value) + '</td></tr>';
                    }
                    html += '</table>';
                }

                if (feature.geometry && feature.geometry.wkt) {
                    html += '<div class="geometry-preview">';
                    html += '<strong>' + escapeHtml(feature.geometry.type || 'Geometry') + ':</strong> ';
                    const wkt = feature.geometry.wkt;
                    html += escapeHtml(wkt.length > 200 ? wkt.substring(0, 200) + '...' : wkt);
                    html += '</div>';
                }

                html += '</div>';
                return html;
            }

            // Whether the gazetteer block has anything worth showing. Only admin or
            // bearing constitute location content — the sources list is empty without
            // them, and the dataset license alone is not location context. Guards
            // against an empty "Ort & Umgebung" box for points with no coverage.
            function hasGazetteerContent(gaz) {
                return !!(gaz && (gaz.admin || gaz.bearing));
            }

            // Renders the location-context block: administrative hierarchy (with the
            // meaning of each level), bearing, name-source explanations and the
            // dataset attribution — everything the /query response carries under
            // "gazetteer" so the page shows it without a second request.
            function renderGazetteer(gaz) {
                let html = '<div class="gazetteer-block">';
                html += '<h3 class="gazetteer-title">Ort &amp; Umgebung</h3>';

                if (gaz.admin) {
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Verwaltungshierarchie';
                    if (gaz.admin.country_iso) {
                        html += ' <span class="badge">' + escapeHtml(gaz.admin.country_iso) + '</span>';
                    }
                    html += '</div>';
                    const chain = gaz.admin.hierarchy || [];
                    if (chain.length > 0) {
                        html += '<ul class="admin-list">';
                        chain.forEach(function(u) {
                            html += '<li class="admin-item">';
                            html += '<div class="admin-line">';
                            html += '<span class="admin-name">' + escapeHtml(u.name || '-') + '</span>';
                            if (u.name_native && u.name_native !== u.name) {
                                html += ' <span class="admin-native">' + escapeHtml(u.name_native) + '</span>';
                            }
                            html += '<span class="admin-level">L' + escapeHtml(String(u.level)) + '</span>';
                            html += '</div>';
                            const tier = [];
                            if (u.equivalent) tier.push(escapeHtml(u.equivalent));
                            if (u.local_term) tier.push(escapeHtml(u.local_term));
                            if (tier.length > 0) {
                                html += '<div class="admin-tier">' + tier.join(' &middot; ') + '</div>';
                            }
                            if (u.equivalent_description) {
                                html += '<div class="admin-desc">' + escapeHtml(u.equivalent_description) + '</div>';
                            }
                            if (u.name_source) {
                                html += '<div class="admin-src">Name: <code>' + escapeHtml(u.name_source) + '</code></div>';
                            }
                            html += '</li>';
                        });
                        html += '</ul>';
                    }
                    html += '</div>';
                }

                if (gaz.bearing) {
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Peilung</div>';
                    html += '<div class="gaz-bearing">' + escapeHtml(gaz.bearing.label || (gaz.bearing.reference || '')) + '</div>';
                    const meta = [];
                    if (gaz.bearing.class) meta.push(escapeHtml(gaz.bearing.class));
                    if (typeof gaz.bearing.distance_km === 'number') meta.push(gaz.bearing.distance_km.toFixed(1) + ' km');
                    if (gaz.bearing.name_source) meta.push('Name: ' + escapeHtml(gaz.bearing.name_source));
                    if (meta.length > 0) {
                        html += '<div class="gaz-bearing-meta">' + meta.join(' &middot; ') + '</div>';
                    }
                    html += '</div>';
                }

                if (gaz.sources && gaz.sources.length > 0) {
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Namensquellen</div>';
                    html += '<ul class="source-explain-list">';
                    gaz.sources.forEach(function(s) {
                        html += '<li><code>' + escapeHtml(s.code) + '</code> ' + escapeHtml(s.long || s.short || '');
                        if (s.standard) {
                            html += ' <span class="src-standard">(' + escapeHtml(s.standard) + ')</span>';
                        }
                        html += '</li>';
                    });
                    html += '</ul></div>';
                }

                if (gaz.license) {
                    html += '<div class="gaz-section gaz-license">';
                    html += '<strong>Datenlizenz:</strong> ';
                    if (gaz.license.url) {
                        html += '<a href="' + escapeHtml(gaz.license.url) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(gaz.license.name || 'Lizenz') + '</a>';
                    } else {
                        html += escapeHtml(gaz.license.name || '-');
                    }
                    if (gaz.license.attribution) {
                        html += '<div class="gaz-attribution">' + escapeHtml(gaz.license.attribution) + '</div>';
                    }
                    html += '</div>';
                }

                html += '</div>';
                return html;
            }

            function formatValue(value) {
                if (value === null || value === undefined) return '<em>null</em>';
                if (typeof value === 'object') return '<code>' + escapeHtml(JSON.stringify(value)) + '</code>';
                const str = String(value);
                // A hex colour (#RGB / #RRGGBB / #RRGGBBAA) gets a swatch before the
                // code. The regex guarantees str is only '#' + hex digits, so it is
                // safe to inline into the style attribute.
                if (/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(str)) {
                    return '<span class="value-swatch" style="background:' + str + '"></span>' +
                           '<span class="value-color">' + escapeHtml(str) + '</span>';
                }
                return escapeHtml(str);
            }

            function escapeHtml(str) {
                if (!str) return '';
                return String(str)
                    .replace(/&/g, '&amp;')
                    .replace(/</g, '&lt;')
                    .replace(/>/g, '&gt;')
                    .replace(/"/g, '&quot;')
                    .replace(/'/g, '&#39;');
            }
        })();
    </script>
</body>
</html>`

// handleFrontend serves the coordinate query frontend.
func (s *Server) handleFrontend(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(frontendHTML))
}
