package http

import (
	"html"
	"net/http"
	"strings"
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

        input, select, textarea {
            width: 100%;
            padding: 0.625rem 0.75rem;
            font-size: 1rem;
            border: 1px solid var(--border);
            border-radius: var(--radius);
            background: var(--card);
            color: var(--text);
            transition: border-color 0.15s, box-shadow 0.15s;
        }

        input:focus, select:focus, textarea:focus {
            outline: none;
            border-color: var(--primary);
            box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.1);
        }

        input::placeholder, textarea::placeholder {
            color: var(--text-muted);
        }

        textarea {
            font-family: 'SF Mono', Monaco, monospace;
            font-size: 0.875rem;
            resize: vertical;
            min-height: 10rem;
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

        /* Tab bar (Einzelkoordinate / Batch) */
        .tabs {
            display: flex;
            gap: 0.25rem;
            margin-bottom: 1rem;
            border-bottom: 1px solid var(--border);
        }

        .tab {
            min-height: 44px;
            padding: 0.625rem 1rem;
            font-size: 0.9375rem;
            font-weight: 500;
            color: var(--text-muted);
            background: none;
            border: none;
            border-bottom: 2px solid transparent;
            cursor: pointer;
        }

        .tab:hover {
            color: var(--text);
        }

        .tab[aria-selected="true"] {
            color: var(--primary);
            border-bottom-color: var(--primary);
        }

        .tab:focus-visible {
            outline: 2px solid var(--primary);
            outline-offset: 2px;
        }

        /* Options accordion (collapsed by default, reuses the source-card
           expand/collapse mechanics). */
        .options-card {
            margin-bottom: 1rem;
        }

        .options-card .source-content {
            padding: 0.75rem 1rem;
        }

        .checkbox-row {
            display: flex;
            align-items: center;
            gap: 0.5rem;
            min-height: 32px;
            font-weight: 400;
            margin-bottom: 0;
            cursor: pointer;
        }

        .checkbox-row input[type="checkbox"] {
            width: 1.05rem;
            height: 1.05rem;
            margin: 0;
            accent-color: var(--primary);
        }

        /* Batch result table */
        .batch-table-wrap {
            overflow-x: auto;
        }

        .batch-table {
            width: 100%;
            font-size: 0.8125rem;
            border-collapse: collapse;
        }

        .batch-table th, .batch-table td {
            text-align: left;
            padding: 0.375rem 0.5rem;
            border-bottom: 1px solid var(--border);
            white-space: nowrap;
        }

        .batch-table th {
            font-weight: 500;
            color: var(--text-muted);
        }

        .batch-table td.batch-err {
            color: var(--error);
            white-space: normal;
        }

        .batch-actions {
            display: flex;
            flex-wrap: wrap;
            gap: 0.5rem;
            margin-bottom: 1rem;
        }

        .batch-actions .btn {
            width: auto;
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

        #results, #batchResults {
            display: none;
        }

        #results.active, #batchResults.active {
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
            white-space: nowrap;
            font-variant-numeric: tabular-nums;
        }

        .badge {
            display: inline-flex;
            align-items: center;
            flex: none;
            white-space: nowrap;
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

        /* Color-valued properties (e.g. a #RRGGBB class color) get a swatch. */
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

        .admin-src, .gaz-bearing-meta, .gaz-elevation-meta, .gaz-exposure-meta {
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

        .gaz-bearing, .gaz-elevation, .gaz-exposure {
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

        .footer-version {
            margin-top: 0.4rem;
            opacity: 0.65;
            font-variant-numeric: tabular-nums;
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

        <div class="tabs" role="tablist" aria-label="Abfrage-Modus">
            <button type="button" class="tab" id="tabSingle" role="tab" aria-selected="true" aria-controls="panelSingle">Einzelkoordinate</button>
            <button type="button" class="tab" id="tabBatch" role="tab" aria-selected="false" aria-controls="panelBatch" tabindex="-1">Batch</button>
        </div>

        <div class="error" id="error" role="alert"></div>

        <div class="loading" id="loading" role="status" aria-live="polite">
            <div class="spinner"></div>
            <p>Abfrage wird ausgeführt...</p>
        </div>

        <div id="panelSingle" role="tabpanel" aria-labelledby="tabSingle">
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
                        <option value="mgrs">MGRS (Military Grid Reference System)</option>
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
                <div class="form-group" id="groupMgrs" style="display:none">
                    <label for="coordMgrs" id="labelMgrs">MGRS</label>
                    <input type="text" id="coordMgrs" name="mgrs" placeholder="32U NA 01234 56789" autocomplete="off">
                </div>

                <div class="options-card source-card">
                    <div class="source-header" role="button" tabindex="0" aria-expanded="false" aria-controls="optionsSingle">
                        <div class="source-main"><span class="source-name">Optionen</span></div>
                        <svg class="toggle-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true" focusable="false"><path d="M6 9l6 6 6-6"/></svg>
                    </div>
                    <div class="source-content" id="optionsSingle">
                        <label class="checkbox-row" for="withGazetteer">
                            <input type="checkbox" id="withGazetteer" checked>
                            Gazetteer (Ort &amp; Umgebung)
                        </label>
                        <label class="checkbox-row" for="withSources">
                            <input type="checkbox" id="withSources" checked>
                            Datenquellen (Packages)
                        </label>
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
        </div>

        <div id="panelBatch" role="tabpanel" aria-labelledby="tabBatch" hidden>
        <div class="card">
            <h2 class="card-title">Batch-Abfrage</h2>
            <form id="batchForm">
                <div class="form-group">
                    <label for="batchSrid">Koordinatensystem</label>
                    <select id="batchSrid" name="batchSrid">
                        <option value="4326">WGS 84 (EPSG:4326) - GPS</option>
                        <option value="3857">Web Mercator (EPSG:3857)</option>
                        <option value="25832">ETRS89 / UTM Zone 32N (EPSG:25832)</option>
                        <option value="25833">ETRS89 / UTM Zone 33N (EPSG:25833)</option>
                        <option value="31466">DHDN / Gauß-Krüger Zone 2 (EPSG:31466)</option>
                        <option value="31467">DHDN / Gauß-Krüger Zone 3 (EPSG:31467)</option>
                        <option value="mgrs">MGRS (Military Grid Reference System)</option>
                    </select>
                </div>

                <div class="form-group">
                    <label for="batchInput" id="batchInputLabel">Koordinaten (eine pro Zeile, optional mit vorangestellter id)</label>
                    <textarea id="batchInput" rows="8" spellcheck="false" autocomplete="off" placeholder="52.52, 13.405&#10;48.137; 11.575&#10;P-001; 47,3769; 8,5417"></textarea>
                </div>

                <div class="options-card source-card">
                    <div class="source-header" role="button" tabindex="0" aria-expanded="false" aria-controls="optionsBatch">
                        <div class="source-main"><span class="source-name">Optionen</span></div>
                        <svg class="toggle-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true" focusable="false"><path d="M6 9l6 6 6-6"/></svg>
                    </div>
                    <div class="source-content" id="optionsBatch">
                        <label class="checkbox-row" for="batchWithGazetteer">
                            <input type="checkbox" id="batchWithGazetteer" checked>
                            Gazetteer (Ort &amp; Umgebung)
                        </label>
                        <label class="checkbox-row" for="batchWithSources">
                            <input type="checkbox" id="batchWithSources" checked>
                            Datenquellen (Packages)
                        </label>
                    </div>
                </div>

                <div class="btn-row">
                    <button type="submit" class="btn" id="batchSubmitBtn">Abfragen</button>
                    <button type="button" class="btn btn-secondary" id="batchCsvUploadBtn">CSV hochladen</button>
                    <button type="button" class="btn btn-secondary" id="batchClearBtn">Leeren</button>
                </div>
                <input type="file" id="batchFile" accept=".csv,text/csv" hidden>
            </form>
        </div>

        <div id="batchResults">
            <div class="card">
                <h2 class="card-title">Batch-Ergebnisse</h2>
                <div class="result-header" role="status" aria-live="polite">
                    <span class="result-stats" id="batchStats"></span>
                </div>
                <div class="batch-actions">
                    <button type="button" class="btn btn-secondary" id="batchDownloadBtn">CSV herunterladen</button>
                    <button type="button" class="btn btn-secondary" id="batchCopyCsvBtn">Als CSV kopieren</button>
                    <button type="button" class="btn btn-secondary" id="batchCopyJsonBtn">Als JSON kopieren</button>
                </div>
                <div class="batch-table-wrap" id="batchTableWrap"></div>
            </div>
        </div>
        </div>

        <footer>
            <a href="/docs">API Dokumentation</a> &middot;
            <a href="/openapi.json">OpenAPI Spec</a> &middot;
            <a href="/health">Health Status</a>
            <div class="footer-version">ortus __ORTUS_VERSION__</div>
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
            const groupMgrs = document.getElementById('groupMgrs');
            const coordMgrs = document.getElementById('coordMgrs');
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
            const withGazetteer = document.getElementById('withGazetteer');
            const withSources = document.getElementById('withSources');
            const tabSingle = document.getElementById('tabSingle');
            const tabBatch = document.getElementById('tabBatch');
            const panelSingle = document.getElementById('panelSingle');
            const panelBatch = document.getElementById('panelBatch');
            const batchForm = document.getElementById('batchForm');
            const batchSrid = document.getElementById('batchSrid');
            const batchInput = document.getElementById('batchInput');
            const batchFile = document.getElementById('batchFile');
            const batchCsvUploadBtn = document.getElementById('batchCsvUploadBtn');
            const batchClearBtn = document.getElementById('batchClearBtn');
            const batchSubmitBtn = document.getElementById('batchSubmitBtn');
            const batchWithGazetteer = document.getElementById('batchWithGazetteer');
            const batchWithSources = document.getElementById('batchWithSources');
            const batchResults = document.getElementById('batchResults');
            const batchStats = document.getElementById('batchStats');
            const batchTableWrap = document.getElementById('batchTableWrap');
            const batchDownloadBtn = document.getElementById('batchDownloadBtn');
            const batchCopyCsvBtn = document.getElementById('batchCopyCsvBtn');
            const batchCopyJsonBtn = document.getElementById('batchCopyJsonBtn');

            // --- Tabs (Einzelkoordinate / Batch) ---
            function selectTab(batch) {
                tabSingle.setAttribute('aria-selected', batch ? 'false' : 'true');
                tabBatch.setAttribute('aria-selected', batch ? 'true' : 'false');
                tabSingle.tabIndex = batch ? -1 : 0;
                tabBatch.tabIndex = batch ? 0 : -1;
                panelSingle.hidden = batch;
                panelBatch.hidden = !batch;
                hideError();
            }
            tabSingle.addEventListener('click', function() { selectTab(false); });
            tabBatch.addEventListener('click', function() { selectTab(true); });
            [tabSingle, tabBatch].forEach(function(tab) {
                tab.addEventListener('keydown', function(e) {
                    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
                        e.preventDefault();
                        const other = tab === tabSingle ? tabBatch : tabSingle;
                        selectTab(other === tabBatch);
                        other.focus();
                    }
                });
            });

            // --- Accordions: one binder for the collapsible headers (options cards
            // now, per-source result cards after each render). ---
            function bindAccordion(header) {
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
            }
            document.querySelectorAll('.options-card .source-header').forEach(bindAccordion);

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

            // Update labels when SRID changes. MGRS has no numeric SRID and no
            // separate X/Y fields — it swaps coordGrid out for a single text field
            // (groupMgrs) instead of reusing coordX/coordY.
            sridSelect.addEventListener('change', function() {
                const isMgrs = this.value === 'mgrs';
                groupMgrs.style.display = isMgrs ? '' : 'none';
                coordGrid.style.display = isMgrs ? 'none' : '';
                coordMgrs.required = isMgrs;
                coordX.required = !isMgrs;
                coordY.required = !isMgrs;

                if (!isMgrs) {
                    const config = sridConfig[this.value] || sridConfig['4326'];
                    labelX.textContent = config.xLabel;
                    labelY.textContent = config.yLabel;
                    coordX.placeholder = config.xPlaceholder;
                    coordY.placeholder = config.yPlaceholder;
                    applyFieldOrder(this.value);
                }

                // Clear values when switching coordinate systems
                coordX.value = '';
                coordY.value = '';
                coordMgrs.value = '';
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
                coordMgrs.value = '';
                hideError();
                results.classList.remove('active');
            });

            // Form submit
            form.addEventListener('submit', async function(e) {
                e.preventDefault();
                hideError();

                const srid = sridSelect.value;
                let url;

                if (srid === 'mgrs') {
                    const mgrs = coordMgrs.value.trim();
                    if (!mgrs) {
                        showError('Bitte geben Sie eine gültige MGRS-Koordinate ein.');
                        return;
                    }
                    url = '/api/v1/query?mgrs=' + encodeURIComponent(mgrs);
                } else {
                    const x = parseFloat(coordX.value.replace(',', '.'));
                    const y = parseFloat(coordY.value.replace(',', '.'));

                    if (isNaN(x) || isNaN(y)) {
                        showError('Bitte geben Sie gültige Koordinaten ein.');
                        return;
                    }

                    // Build query URL with proper URL encoding
                    url = '/api/v1/query?srid=' + encodeURIComponent(srid);
                    if (srid === '4326') {
                        url += '&lon=' + encodeURIComponent(x) + '&lat=' + encodeURIComponent(y);
                    } else {
                        url += '&x=' + encodeURIComponent(x) + '&y=' + encodeURIComponent(y);
                    }
                }

                // The switches are opt-out (server default: on) — only an unchecked
                // box adds its parameter.
                if (!withGazetteer.checked) url += '&with-gazetteer=0';
                if (!withSources.checked) url += '&with-sources=0';

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
                // Header info. For WGS84 we show Lat/Lon (the conventional geographic
                // order). For MGRS input we show the same Lat/Lon, from the response's
                // wgs84 block — the underlying UTM zone/SRID (EPSG:326xx/327xx) isn't
                // meaningful to a caller who typed an MGRS string. For any other
                // projected SRID we show the entered X/Y plus the reprojected WGS84
                // lat/lon from the response's wgs84 block.
                const coord = data.coordinate;
                if (srid === '4326') {
                    resultCoord.textContent = 'Lat: ' + coord.y.toFixed(6) + ', Lon: ' + coord.x.toFixed(6);
                } else if (srid === 'mgrs' && data.wgs84) {
                    resultCoord.textContent = 'Lat: ' + data.wgs84.lat.toFixed(6) + ', Lon: ' + data.wgs84.lon.toFixed(6);
                } else {
                    let txt = 'X: ' + coord.x.toFixed(2) + ', Y: ' + coord.y.toFixed(2) + ' (EPSG:' + coord.srid + ')';
                    if (data.wgs84) {
                        txt += ' · WGS84 Lat: ' + data.wgs84.lat.toFixed(6) + ', Lon: ' + data.wgs84.lon.toFixed(6);
                    }
                    resultCoord.textContent = txt;
                }

                resultStats.textContent = data.total_features + ' Feature(s) in ' + data.processing_time_ms + 'ms';

                let html = '';

                // Location context (gazetteer): admin hierarchy, islands, mountains,
                // elevation, bearing, exposure, name-source explanations and attribution.
                // Present whenever the query point could be reprojected to WGS84 (any
                // SRID the transformer supports, not just 4326) and the feature is
                // enabled — but only rendered when it actually has location content
                // (an uncovered point with no anchor would otherwise be an empty box).
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
                // Scoped to the freshly rendered result content: the static options
                // accordions are bound once at startup and must not accumulate
                // duplicate listeners on every render.
                resultContent.querySelectorAll('.source-header').forEach(bindAccordion);

                results.classList.add('active');
            }

            function renderSource(pkg, expanded) {
                let html = '<div class="source-card' + (expanded ? ' expanded' : '') + '">';
                html += '<div class="source-header" role="button" tabindex="0" aria-expanded="' + (expanded ? 'true' : 'false') + '">';
                html += '<div class="source-main">';
                html += '<span class="source-name">' + escapeHtml(pkg.source_name || pkg.source_id) + '</span>';
                html += '<div class="source-meta">';
                html += '<span class="badge">' + (pkg.feature_count === 1 ? '1 Feature' : pkg.feature_count + ' Features') + '</span>';
                html += '<span class="meta-sep" aria-hidden="true">&middot;</span>';
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

            // Whether the gazetteer block has anything worth showing. Admin, an
            // island, elevation or a bearing constitute location content — the
            // sources list is empty without them, and the dataset license alone is
            // not location context. Guards against an empty "Ort & Umgebung" box for
            // points with no coverage.
            function hasGazetteerContent(gaz) {
                return !!(gaz && (gaz.admin || (gaz.islands && gaz.islands.length) || (gaz.mountains && (gaz.mountains.mountain || gaz.mountains.range)) || gaz.elevation || gaz.bearing || gaz.exposure));
            }

            // Renders the location-context block: administrative hierarchy (with the
            // meaning of each level), the containing island(s), elevation, bearing,
            // name-source explanations and the dataset attribution — everything the
            // /query response carries under "gazetteer" so the page shows it without
            // a second request.
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

                if (gaz.islands && gaz.islands.length > 0) {
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Insel' + (gaz.islands.length > 1 ? 'n' : '') + '</div>';
                    html += '<ul class="admin-list">';
                    gaz.islands.forEach(function(is) {
                        html += '<li class="admin-item">';
                        html += '<div class="admin-line">';
                        html += '<span class="admin-name">' + escapeHtml(is.name || '-') + '</span>';
                        if (is.name_native && is.name_native !== is.name) {
                            html += ' <span class="admin-native">' + escapeHtml(is.name_native) + '</span>';
                        }
                        html += '</div>';
                        if (is.name_source) {
                            html += '<div class="admin-src">Name: <code>' + escapeHtml(is.name_source) + '</code></div>';
                        }
                        html += '</li>';
                    });
                    html += '</ul></div>';
                }

                // Mountains: the smallest containing single mountain (Berg) and range
                // (Gebirge), each optional. Berg carries a summit elevation.
                if (gaz.mountains && (gaz.mountains.mountain || gaz.mountains.range)) {
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Berg / Gebirge</div>';
                    html += '<ul class="admin-list">';
                    [{ m: gaz.mountains.mountain, kind: 'Berg' }, { m: gaz.mountains.range, kind: 'Gebirge' }].forEach(function(row) {
                        var m = row.m;
                        if (!m) { return; }
                        html += '<li class="admin-item">';
                        html += '<div class="admin-line">';
                        html += '<span class="admin-name">' + escapeHtml(m.name || '-') + '</span>';
                        html += ' <span class="badge">' + row.kind + '</span>';
                        if (typeof m.elevation === 'number') {
                            html += ' <span class="admin-native">' + Math.round(m.elevation) + ' m</span>';
                        }
                        if (m.name_native && m.name_native !== m.name) {
                            html += ' <span class="admin-native">' + escapeHtml(m.name_native) + '</span>';
                        }
                        html += '</div>';
                        if (m.name_source) {
                            html += '<div class="admin-src">Name: <code>' + escapeHtml(m.name_source) + '</code></div>';
                        }
                        html += '</li>';
                    });
                    html += '</ul></div>';
                }

                // Elevation renders inside the gazetteer block, before the bearing.
                if (gaz.elevation) {
                    const e = gaz.elevation;
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Höhe</div>';
                    let elevText;
                    if (e.sea_level) {
                        elevText = 'Meeresspiegel (0 m)';
                    } else {
                        elevText = (typeof e.meters === 'number' ? e.meters.toFixed(0) : '-') + ' m ü. NN';
                    }
                    html += '<div class="gaz-elevation">' + escapeHtml(elevText) + '</div>';
                    const emeta = [];
                    if (typeof e.accuracy_m === 'number' && e.accuracy_m > 0) emeta.push('±' + e.accuracy_m.toFixed(0) + ' m');
                    if (e.accuracy_basis) emeta.push(escapeHtml(e.accuracy_basis));
                    if (e.vertical_datum) emeta.push(escapeHtml(e.vertical_datum));
                    if (emeta.length > 0) {
                        html += '<div class="gaz-elevation-meta">' + emeta.join(' &middot; ') + '</div>';
                    }
                    if (e.source && e.source.name) {
                        html += '<div class="gaz-attribution">Quelle: ';
                        const srcUrl = httpUrl(e.source.url);
                        if (srcUrl) {
                            html += '<a href="' + escapeHtml(srcUrl) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(e.source.name) + '</a>';
                        } else {
                            html += escapeHtml(e.source.name);
                        }
                        html += '</div>';
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

                // Exposure (terrain slope + aspect) renders next to the bearing.
                if (gaz.exposure) {
                    const x = gaz.exposure;
                    html += '<div class="gaz-section">';
                    html += '<div class="gaz-label">Exposition</div>';
                    let expText;
                    if (x.flat) {
                        expText = 'eben (Neigung ' + (typeof x.slope_deg === 'number' ? x.slope_deg.toFixed(1) : '?') + '°)';
                    } else {
                        const dir = x.aspect_compass ? escapeHtml(x.aspect_compass) : '';
                        const asp = typeof x.aspect_deg === 'number' ? ' (' + x.aspect_deg.toFixed(0) + '°)' : '';
                        expText = (dir ? dir + asp + '-Exposition' : 'Exposition') +
                            ', ' + (typeof x.slope_deg === 'number' ? x.slope_deg.toFixed(1) : '?') + '° Neigung';
                    }
                    html += '<div class="gaz-exposure">' + expText + '</div>';
                    const xmeta = [];
                    if (typeof x.slope_percent === 'number') xmeta.push(x.slope_percent.toFixed(0) + ' %');
                    if (typeof x.sample_spacing_m === 'number') xmeta.push('Raster ~' + x.sample_spacing_m.toFixed(0) + ' m');
                    if (xmeta.length > 0) {
                        html += '<div class="gaz-exposure-meta">' + xmeta.join(' &middot; ') + '</div>';
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
                // A hex color (#RGB / #RRGGBB / #RRGGBBAA) gets a swatch before the
                // hex value. The regex guarantees str is only '#' + hex digits, so
                // it is safe to inline into the style attribute. The swatch is
                // decorative (aria-hidden) — the hex value beside it is the real one.
                if (/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(str)) {
                    return '<span class="value-swatch" style="background-color:' + str + ';" aria-hidden="true"></span>' +
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

            // Returns the URL only when it is an explicit http(s) URL, else ''. Guards
            // against non-http schemes (e.g. javascript:) becoming clickable links,
            // even though these URLs come from trusted dataset config.
            function httpUrl(u) {
                return /^https?:\/\//i.test(String(u || '')) ? u : '';
            }

            // ===================== Batch tab =====================

            // The last successful batch response + its input SRID mode, for the
            // CSV/JSON export buttons.
            let lastBatch = null;

            // SRID-aware textarea placeholder so the expected line format is visible
            // before typing: coordinate per line, optional leading id, tolerant
            // separators (comma/semicolon/space, German decimal comma).
            const batchPlaceholders = {
                '4326': '52.52, 13.405\n48.137; 11.575\nP-001; 47,3769; 8,5417',
                'mgrs': '32U NA 01234 56789\nP-001; 33U VP 12345 67890',
                'projected': '389524, 5820270\n390100; 5821400\nP-001; 389524; 5820270'
            };
            function applyBatchPlaceholder() {
                batchInput.placeholder = batchPlaceholders[batchSrid.value] || batchPlaceholders.projected;
            }
            batchSrid.addEventListener('change', applyBatchPlaceholder);
            applyBatchPlaceholder();

            // parseBatchLines turns the textarea content into batch points. One
            // coordinate per line with an optional leading id; a leading header line
            // (no digits, e.g. "id,lat,lon") is skipped; empty lines are ignored.
            // Returns {points} or {error} naming the first unreadable line.
            function parseBatchLines(text, srid) {
                const isMgrs = srid === 'mgrs';
                const lines = String(text || '').split(/\r?\n/);
                const points = [];
                for (let n = 0; n < lines.length; n++) {
                    const line = lines[n].trim();
                    if (!line) continue;
                    if (points.length === 0 && !/[0-9]/.test(line)) continue; // header line
                    const p = isMgrs ? parseMgrsLine(line) : parseCoordLine(line, srid);
                    if (!p) {
                        return { error: 'Zeile ' + (n + 1) + ' ist nicht lesbar: "' + line + '"' };
                    }
                    points.push(p);
                }
                if (points.length === 0) {
                    return { error: 'Keine Koordinaten gefunden — eine Koordinate pro Zeile eintragen (siehe Platzhalter).' };
                }
                return { points: points };
            }

            // One MGRS reference per line, optionally "id; <mgrs>" (or comma).
            function parseMgrsLine(line) {
                const parts = line.split(/[;,]/).map(function(s) { return s.trim(); }).filter(Boolean);
                if (parts.length === 1) return { mgrs: parts[0] };
                if (parts.length === 2) return { id: parts[0], mgrs: parts[1] };
                return null;
            }

            // "lat,lon" (WGS84) / "x,y" (projected), optionally with a leading id —
            // same separator heuristics as the single-tab smart paste (semicolons
            // allow German decimal commas inside the numbers).
            function parseCoordLine(line, srid) {
                let parts, commaIsDecimal;
                if (line.indexOf(';') >= 0) {
                    parts = line.split(';'); commaIsDecimal = true;
                } else if (line.indexOf(',') >= 0 && line.indexOf('.') >= 0) {
                    parts = line.split(','); commaIsDecimal = false;
                } else if (line.indexOf(',') >= 0 && /\s/.test(line)) {
                    parts = line.split(/\s+/); commaIsDecimal = true;
                } else if (line.indexOf(',') >= 0) {
                    parts = line.split(','); commaIsDecimal = false;
                } else {
                    parts = line.split(/\s+/); commaIsDecimal = false;
                }
                parts = parts.map(function(s) { return s.trim(); }).filter(Boolean);
                let id = null;
                if (parts.length >= 3) {
                    id = parts[0];
                    parts = parts.slice(1);
                }
                if (parts.length < 2) return null;
                const a = normNum(parts[0], commaIsDecimal);
                const b = normNum(parts[1], commaIsDecimal);
                if (a === null || b === null) return null;
                // Visual order matches the single tab: WGS84 is lat first, projected
                // systems are x (Rechtswert) first.
                const p = srid === '4326'
                    ? { lat: parseFloat(a), lon: parseFloat(b) }
                    : { x: parseFloat(a), y: parseFloat(b) };
                if (id !== null) p.id = id;
                return p;
            }

            // CSV upload fills the textarea (single source of truth — the user sees
            // exactly what will be parsed on submit).
            batchCsvUploadBtn.addEventListener('click', function() { batchFile.click(); });
            batchFile.addEventListener('change', function() {
                const f = batchFile.files && batchFile.files[0];
                if (!f) return;
                const reader = new FileReader();
                reader.onload = function() { batchInput.value = String(reader.result || ''); };
                reader.onerror = function() { showError('CSV-Datei konnte nicht gelesen werden.'); };
                reader.readAsText(f);
                batchFile.value = '';
            });

            batchClearBtn.addEventListener('click', function() {
                batchInput.value = '';
                hideError();
                batchResults.classList.remove('active');
            });

            batchForm.addEventListener('submit', async function(e) {
                e.preventDefault();
                hideError();

                const srid = batchSrid.value;
                const parsed = parseBatchLines(batchInput.value, srid);
                if (parsed.error) {
                    showError(parsed.error);
                    return;
                }
                const body = { points: parsed.points };
                if (srid !== 'mgrs' && srid !== '4326') {
                    body.srid = parseInt(srid, 10);
                }
                // Opt-out switches, consistent with the single tab: only send the
                // field when the box is unchecked (server default: on).
                if (!batchWithGazetteer.checked) body['with-gazetteer'] = false;
                if (!batchWithSources.checked) body['with-sources'] = false;

                batchSubmitBtn.disabled = true;
                loading.classList.add('active');
                batchResults.classList.remove('active');

                try {
                    const response = await fetch('/api/v1/query/batch', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(body)
                    });
                    if (!response.ok) {
                        let errorMessage = 'Batch-Abfrage fehlgeschlagen';
                        try {
                            const errorData = await response.json();
                            errorMessage = errorData.message || errorData.error || errorMessage;
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
                    displayBatchResults(data, srid);
                } catch (err) {
                    showError(err.message);
                } finally {
                    batchSubmitBtn.disabled = false;
                    loading.classList.remove('active');
                }
            });

            // Compact per-point table: id, coordinate, place summary, feature count
            // (or the per-point error). Details live in the CSV/JSON exports.
            function displayBatchResults(data, srid) {
                lastBatch = { data: data, srid: srid };
                const items = data.results || [];
                let errs = 0;
                items.forEach(function(i) { if (i.error) errs++; });
                batchStats.textContent = items.length + ' Punkt(e) in ' + data.processing_time_ms + ' ms' +
                    (errs > 0 ? ' · ' + errs + ' Fehler' : '');

                let html = '<table class="batch-table"><thead><tr>' +
                    '<th>id</th><th>Koordinate</th><th>Ort</th><th>Features</th>' +
                    '</tr></thead><tbody>';
                items.forEach(function(item) {
                    html += '<tr><td>' + escapeHtml(item.id || '') + '</td>';
                    if (item.error) {
                        html += '<td>—</td><td class="batch-err" colspan="2">Fehler: ' +
                            escapeHtml(item.error.message || 'unbekannt') + '</td>';
                    } else {
                        html += '<td>' + escapeHtml(batchCoordText(item)) + '</td>';
                        html += '<td>' + escapeHtml(batchPlaceSummary(item)) + '</td>';
                        html += '<td>' + (typeof item.total_features === 'number' ? item.total_features : '—') + '</td>';
                    }
                    html += '</tr>';
                });
                html += '</tbody></table>';
                batchTableWrap.innerHTML = html;
                batchResults.classList.add('active');
            }

            function batchCoordText(item) {
                if (item.wgs84) {
                    return item.wgs84.lat.toFixed(5) + ', ' + item.wgs84.lon.toFixed(5);
                }
                if (item.coordinate) {
                    return item.coordinate.x + ', ' + item.coordinate.y + ' (EPSG:' + item.coordinate.srid + ')';
                }
                return '—';
            }

            // Short place summary: country code + the two most specific admin names.
            function batchPlaceSummary(item) {
                const gaz = item.gazetteer;
                if (!gaz || !gaz.admin) return '';
                const parts = [];
                if (gaz.admin.country_iso) parts.push(gaz.admin.country_iso);
                (gaz.admin.hierarchy || []).slice(-2).forEach(function(u) {
                    if (u.name) parts.push(u.name);
                });
                return parts.join(' · ');
            }

            // buildBatchCSV flattens the batch response into fixed columns: the echo
            // id, the input coordinate, the WGS84 coordinate, the per-point error and
            // a gazetteer summary (country, admin path, elevation, bearing) plus the
            // PiP feature count. Source properties stay in the JSON export.
            function buildBatchCSV(data, srid) {
                const isGeo = srid === '4326' || srid === 'mgrs';
                const cols = ['id', isGeo ? 'lat' : 'x', isGeo ? 'lon' : 'y',
                    'wgs84_lat', 'wgs84_lon', 'error', 'country_iso', 'admin_path',
                    'elevation_m', 'bearing', 'total_features'];
                const rows = [cols.map(csvField).join(',')];
                (data.results || []).forEach(function(item) {
                    const gaz = item.gazetteer || {};
                    const admin = gaz.admin || {};
                    let inA = '', inB = '';
                    if (srid === '4326' && item.coordinate) {
                        inA = item.coordinate.y; inB = item.coordinate.x;
                    } else if (srid === 'mgrs' && item.wgs84) {
                        inA = item.wgs84.lat; inB = item.wgs84.lon;
                    } else if (item.coordinate) {
                        inA = item.coordinate.x; inB = item.coordinate.y;
                    }
                    const adminPath = (admin.hierarchy || []).map(function(u) { return u.name || ''; }).join(' > ');
                    let elev = '';
                    if (gaz.elevation) {
                        elev = gaz.elevation.sea_level ? 0 :
                            (typeof gaz.elevation.meters === 'number' ? gaz.elevation.meters : '');
                    }
                    rows.push([
                        item.id || '', inA, inB,
                        item.wgs84 ? item.wgs84.lat : '', item.wgs84 ? item.wgs84.lon : '',
                        item.error ? (item.error.message || 'error') : '',
                        admin.country_iso || '', adminPath, elev,
                        gaz.bearing ? (gaz.bearing.label || '') : '',
                        typeof item.total_features === 'number' ? item.total_features : ''
                    ].map(csvField).join(','));
                });
                return rows.join('\r\n') + '\r\n';
            }

            function csvField(v) {
                const s = String(v === null || v === undefined ? '' : v);
                return /[",;\n\r]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
            }

            batchDownloadBtn.addEventListener('click', function() {
                if (!lastBatch) return;
                const blob = new Blob([buildBatchCSV(lastBatch.data, lastBatch.srid)], { type: 'text/csv;charset=utf-8' });
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = 'ortus-batch.csv';
                document.body.appendChild(a);
                a.click();
                a.remove();
                setTimeout(function() { URL.revokeObjectURL(url); }, 1000);
            });

            // Clipboard needs a secure context (HTTPS or localhost) — otherwise show
            // a clear message instead of failing silently.
            function copyText(btn, text) {
                if (!navigator.clipboard || !navigator.clipboard.writeText) {
                    showError('Die Zwischenablage ist in diesem Kontext nicht verfügbar (HTTPS oder localhost erforderlich).');
                    return;
                }
                navigator.clipboard.writeText(text).then(function() {
                    const orig = btn.textContent;
                    btn.textContent = 'Kopiert ✓';
                    setTimeout(function() { btn.textContent = orig; }, 1500);
                }, function() {
                    showError('Kopieren fehlgeschlagen.');
                });
            }
            batchCopyCsvBtn.addEventListener('click', function() {
                if (lastBatch) copyText(batchCopyCsvBtn, buildBatchCSV(lastBatch.data, lastBatch.srid));
            });
            batchCopyJsonBtn.addEventListener('click', function() {
                if (lastBatch) copyText(batchCopyJsonBtn, JSON.stringify(lastBatch.data, null, 2));
            });
        })();
    </script>
</body>
</html>`

// renderFrontend substitutes the build version into the footer placeholder once,
// at server construction. The version is HTML-escaped (it comes from a trusted
// -ldflags value, but escaping keeps the template injection-safe regardless).
func renderFrontend(version string) []byte {
	return []byte(strings.Replace(frontendHTML, "__ORTUS_VERSION__", html.EscapeString(version), 1))
}

// handleFrontend serves the pre-rendered coordinate query frontend. The page is
// built once in NewServer (the version is constant for the server's lifetime),
// so each request only writes the cached bytes.
func (s *Server) handleFrontend(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.frontendPage)
}
