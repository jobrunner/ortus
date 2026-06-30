# ADR-0006: Object Storage und HTTP-Download Integration

## Status

Akzeptiert

## Kontext

GeoPackage-Dateien können mehrere Gigabyte groß sein. Für Container-Deployments ergeben sich folgende Herausforderungen:

1. Container-Images sollten klein und schnell deploybar sein
2. GeoPackages ändern sich unabhängig vom Anwendungscode
3. Mehrere Container-Instanzen benötigen Zugriff auf dieselben Daten
4. Updates der Geodaten sollten ohne Image-Rebuild möglich sein

### Evaluierte Optionen

| Option | Vorteile | Nachteile |
|--------|----------|-----------|
| Im Container-Image | Einfach, keine externe Abhängigkeit | Große Images, Rebuild bei Datenupdate |
| Mounted Volume | Flexibel, Standard | Infrastruktur-abhängig |
| Object Storage (AWS S3/Azure) | Cloud-nativ, skalierbar | Zusätzliche Komplexität |
| HTTP-Download | Universell, einfach | Keine native Listing-Funktion |

## Entscheidung

Wir implementieren eine **Storage-Abstraktion** mit Unterstützung für:
- AWS S3 (und S3-kompatible wie MinIO)
- Azure Blob Storage
- HTTP-Download (mit index.txt)
- Lokales Dateisystem (Fallback)

### Begründung

- **Erweiterbarkeit:** Das Port/Adapter-Pattern ermöglicht es, neue Storage-Backends ohne Änderungen an der Business-Logik hinzuzufügen.
- **Universalität:** HTTP-Download mit index.txt ermöglicht GeoPackage-Bereitstellung von jedem Webserver.
- **Cloud-nativ:** Native AWS S3 und Azure Blob Storage Integration für Cloud-Deployments.
- **Flexibilität:** Je nach Infrastruktur kann der passende Adapter gewählt werden.

### Architektur

```
+-------------------+          +-------------------+          +-------------------+
|   Container       |          |   Storage Backend |          |   Local Disk      |
|   Start           |          | (AWS S3/Azure/HTTP|          |   /data/gpkg/     |
+--------+----------+          +--------+----------+          +--------+----------+
         |                              |                              |
         | 1. Check Storage Config      |                              |
         +----------------------------->|                              |
         |                              |                              |
         | 2. List Objects (.gpkg)      |                              |
         +----------------------------->|                              |
         |<-----------------------------+                              |
         | [file1.gpkg, file2.gpkg]     |                              |
         |                              |                              |
         | 3. Download to local disk    |                              |
         +----------------------------->|                              |
         |<-----------------------------+                              |
         |                              |                              |
         | 4. Process locally           +----------------------------->|
         |                                                             |
         |                                                             |
         | 5. Read-Only öffnen          +----------------------------->|
         |                                                             |
```

### Storage Port Interface

```go
// internal/ports/output/storage.go
type StoragePort interface {
    // List listet alle GeoPackages im Storage
    List(ctx context.Context) ([]StorageObject, error)

    // Download lädt ein GeoPackage in den Writer
    Download(ctx context.Context, key string, dest io.Writer) error

    // GetMetadata gibt Object-Metadaten zurück
    GetMetadata(ctx context.Context, key string) (*StorageObjectMeta, error)
}
```

### Konfiguration

```yaml
storage:
  type: s3  # s3, azure, http, local

  # AWS S3/MinIO
  s3Bucket: "geodata-bucket"
  s3Region: "eu-central-1"
  s3Endpoint: ""  # Für MinIO: "http://minio:9000"
  s3AccessKey: "${AWS_ACCESS_KEY_ID}"
  s3SecretKey: "${AWS_SECRET_ACCESS_KEY}"

  # Azure Blob Storage
  azureContainer: "geodata"
  azureAccountName: "${AZURE_STORAGE_ACCOUNT}"
  azureAccountKey: "${AZURE_STORAGE_KEY}"

  # HTTP-Download
  httpBaseUrl: "https://geodata.example.com/geopackages"
```

## HTTP-Download mit index.txt

Für Szenarien ohne Cloud-Storage kann ein einfacher HTTP-Server verwendet werden.

### index.txt Format

Die Datei `index.txt` enthält alle verfügbaren GeoPackages, eine Datei pro Zeile:

```
gemeinden.gpkg
bodenarten.gpkg
schutzgebiete.gpkg
naturparks.gpkg
```

### HTTP-Adapter Implementation

```go
// internal/adapters/secondary/storage/http.go
package storage

import (
    "bufio"
    "context"
    "fmt"
    "io"
    "net/http"
    "strings"
)

type HTTPAdapter struct {
    baseURL    string
    httpClient *http.Client
}

func NewHTTPAdapter(baseURL string) *HTTPAdapter {
    return &HTTPAdapter{
        baseURL:    strings.TrimSuffix(baseURL, "/"),
        httpClient: &http.Client{Timeout: 30 * time.Minute},
    }
}

// List lädt index.txt und parst die Dateinamen
func (a *HTTPAdapter) List(ctx context.Context) ([]StorageObject, error) {
    indexURL := fmt.Sprintf("%s/index.txt", a.baseURL)

    req, err := http.NewRequestWithContext(ctx, "GET", indexURL, nil)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("fetch index.txt: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("index.txt returned status %d", resp.StatusCode)
    }

    var objects []StorageObject
    scanner := bufio.NewScanner(resp.Body)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "" || strings.HasPrefix(line, "#") {
            continue // Leerzeilen und Kommentare überspringen
        }
        if strings.HasSuffix(line, ".gpkg") {
            objects = append(objects, StorageObject{
                Key: line,
            })
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("scan index.txt: %w", err)
    }

    return objects, nil
}

// Download lädt eine GeoPackage-Datei herunter
func (a *HTTPAdapter) Download(ctx context.Context, key string, dest io.Writer) error {
    fileURL := fmt.Sprintf("%s/%s", a.baseURL, key)

    req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
    if err != nil {
        return fmt.Errorf("create request: %w", err)
    }

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return fmt.Errorf("download %s: %w", key, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("download %s returned status %d", key, resp.StatusCode)
    }

    _, err = io.Copy(dest, resp.Body)
    return err
}

func (a *HTTPAdapter) GetMetadata(ctx context.Context, key string) (*StorageObjectMeta, error) {
    fileURL := fmt.Sprintf("%s/%s", a.baseURL, key)

    req, err := http.NewRequestWithContext(ctx, "HEAD", fileURL, nil)
    if err != nil {
        return nil, err
    }

    resp, err := a.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    return &StorageObjectMeta{
        ContentType:   resp.Header.Get("Content-Type"),
        ContentLength: resp.ContentLength,
        ETag:          resp.Header.Get("ETag"),
    }, nil
}
```

### Beispiel-Setup mit nginx

```nginx
# nginx.conf
server {
    listen 80;
    server_name geodata.example.com;

    location /geopackages/ {
        alias /var/www/geopackages/;
        autoindex off;

        # index.txt und .gpkg-Dateien erlauben
        location ~ \.(txt|gpkg)$ {
            add_header Cache-Control "public, max-age=3600";
        }
    }
}
```

Verzeichnisstruktur:
```
/var/www/geopackages/
├── index.txt
├── gemeinden.gpkg
├── bodenarten.gpkg
└── schutzgebiete.gpkg
```

## AWS S3-Adapter

```go
// internal/adapters/secondary/storage/s3.go
type S3Adapter struct {
    client *s3.Client
    bucket string
}

func (a *S3Adapter) List(ctx context.Context) ([]StorageObject, error) {
    input := &s3.ListObjectsV2Input{
        Bucket: aws.String(a.bucket),
    }

    var objects []StorageObject
    paginator := s3.NewListObjectsV2Paginator(a.client, input)

    for paginator.HasMorePages() {
        page, err := paginator.NextPage(ctx)
        if err != nil {
            return nil, fmt.Errorf("list objects: %w", err)
        }

        for _, obj := range page.Contents {
            if strings.HasSuffix(*obj.Key, ".gpkg") {
                objects = append(objects, StorageObject{
                    Key:          *obj.Key,
                    Size:         *obj.Size,
                    LastModified: *obj.LastModified,
                    ETag:         strings.Trim(*obj.ETag, "\""),
                })
            }
        }
    }

    return objects, nil
}
```

## Download-Strategie

1. **Beim Container-Start:**
   - Storage auf neue/geänderte GeoPackages prüfen
   - ETag/LastModified vergleichen mit lokaler Kopie
   - Nur geänderte Dateien herunterladen

2. **Integritätsprüfung:**
   - ETag als MD5-Hash verwenden (AWS S3-Standard)
   - Nach Download Hash verifizieren

3. **Paralleler Download:**
   - Mehrere GeoPackages gleichzeitig herunterladen
   - Konfigurierbare Parallelität

## Konsequenzen

### Positiv

- **Kleine Container-Images:** Nur Anwendungscode, keine Geodaten
- **Flexible Updates:** Geodaten unabhängig von Releases aktualisierbar
- **Universell:** HTTP-Download funktioniert mit jedem Webserver
- **Cloud-nativ:** Native AWS S3/Azure-Integration für Cloud-Deployments
- **Erweiterbar:** Neue Storage-Backends durch zusätzliche Adapter

### Negativ

- **Startup-Zeit:** Download großer Dateien verzögert Start
- **Abhängigkeit:** Externer Storage muss verfügbar sein
- **Kosten:** Datentransfer-Kosten bei Cloud-Providern

### Mitigationen

- **Startup-Zeit:** Pre-warming durch Init-Container, Readiness-Probe
- **Abhängigkeit:** Graceful Degradation, lokaler Cache
- **Kosten:** Same-Region Deployment, Kompression

## Sicherheitsaspekte

1. **Credentials:**
   - Umgebungsvariablen oder Secret-Manager
   - Niemals in Container-Image oder Code

2. **Netzwerk:**
   - VPC Endpoints für AWS S3/Azure (keine öffentlichen IPs)
   - TLS für Transfers

3. **Berechtigungen:**
   - Minimale IAM-Rechte (nur ListBucket, GetObject)
   - Keine Schreibrechte auf Storage erforderlich

## Referenzen

- [AWS SDK for Go v2](https://aws.github.io/aws-sdk-go-v2/docs/)
- [Azure SDK for Go](https://github.com/Azure/azure-sdk-for-go)
- [MinIO Go Client](https://min.io/docs/minio/linux/developers/go/minio-go.html)
