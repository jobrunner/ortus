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

        .package-card {
            border: 1px solid var(--border);
            border-radius: var(--radius);
            margin-bottom: 0.75rem;
            overflow: hidden;
        }

        .package-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            padding: 0.75rem 1rem;
            background: var(--bg);
            cursor: pointer;
            user-select: none;
        }

        .package-header:hover {
            background: #f1f5f9;
        }

        .package-name {
            font-weight: 500;
            font-size: 0.9375rem;
        }

        .package-meta {
            display: flex;
            gap: 0.75rem;
            font-size: 0.75rem;
            color: var(--text-muted);
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

        .package-content {
            display: none;
            padding: 1rem;
            border-top: 1px solid var(--border);
        }

        .package-card.expanded .package-content {
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
            transition: transform 0.2s;
        }

        .package-card.expanded .toggle-icon {
            transform: rotate(180deg);
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
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Ortus</h1>
            <p>GeoPackage Point-in-Polygon Abfrage</p>
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

                <div class="coord-grid">
                    <div class="form-group">
                        <label for="coordX" id="labelX">Längengrad (Lon)</label>
                        <input type="text" id="coordX" name="x" placeholder="z.B. 13.405" inputmode="decimal" required>
                    </div>
                    <div class="form-group">
                        <label for="coordY" id="labelY">Breitengrad (Lat)</label>
                        <input type="text" id="coordY" name="y" placeholder="z.B. 52.52" inputmode="decimal" required>
                    </div>
                </div>

                <div class="btn-row">
                    <button type="submit" class="btn" id="submitBtn">Abfragen</button>
                    <button type="button" class="btn btn-secondary" id="locationBtn" title="Aktuellen Standort verwenden">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <circle cx="12" cy="12" r="3"/>
                            <path d="M12 2v4m0 12v4M2 12h4m12 0h4"/>
                        </svg>
                    </button>
                    <button type="button" class="btn btn-secondary" id="clearBtn">Leeren</button>
                </div>
            </form>
        </div>

        <div class="error" id="error"></div>

        <div class="loading" id="loading">
            <div class="spinner"></div>
            <p>Abfrage wird ausgeführt...</p>
        </div>

        <div id="results">
            <div class="card">
                <h2 class="card-title">Ergebnisse</h2>
                <div class="result-header">
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

            // Update labels when SRID changes
            sridSelect.addEventListener('change', function() {
                const config = sridConfig[this.value] || sridConfig['4326'];
                labelX.textContent = config.xLabel;
                labelY.textContent = config.yLabel;
                coordX.placeholder = config.xPlaceholder;
                coordY.placeholder = config.yPlaceholder;

                // Clear values when switching coordinate systems
                coordX.value = '';
                coordY.value = '';
            });

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

                // Results content
                if (!data.results || data.results.length === 0) {
                    resultContent.innerHTML = '<div class="no-results">Keine Features an dieser Position gefunden.</div>';
                } else {
                    let html = '';
                    data.results.forEach(function(pkg, idx) {
                        html += renderPackage(pkg, idx === 0);
                    });
                    resultContent.innerHTML = html;

                    // Add click handlers for expand/collapse
                    document.querySelectorAll('.package-header').forEach(function(header) {
                        header.addEventListener('click', function() {
                            this.parentElement.classList.toggle('expanded');
                        });
                    });
                }

                results.classList.add('active');
            }

            function renderPackage(pkg, expanded) {
                let html = '<div class="package-card' + (expanded ? ' expanded' : '') + '">';
                html += '<div class="package-header">';
                html += '<span class="package-name">' + escapeHtml(pkg.package_name || pkg.package_id) + '</span>';
                html += '<div class="package-meta">';
                html += '<span class="badge">' + pkg.feature_count + ' Feature(s)</span>';
                html += '<span>' + pkg.query_time_ms + 'ms</span>';
                html += '<svg class="toggle-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 9l6 6 6-6"/></svg>';
                html += '</div></div>';

                html += '<div class="package-content">';

                if (pkg.features && pkg.features.length > 0) {
                    pkg.features.forEach(function(feature) {
                        html += renderFeature(feature);
                    });
                }

                if (pkg.license) {
                    html += '<div class="license-info">';
                    html += '<strong>Lizenz:</strong> ';
                    if (pkg.license.url) {
                        html += '<a href="' + escapeHtml(pkg.license.url) + '" target="_blank">' + escapeHtml(pkg.license.name || 'Link') + '</a>';
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

            function formatValue(value) {
                if (value === null || value === undefined) return '<em>null</em>';
                if (typeof value === 'object') return '<code>' + escapeHtml(JSON.stringify(value)) + '</code>';
                return escapeHtml(String(value));
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
