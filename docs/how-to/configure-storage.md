# Load from object storage (S3 / Azure / HTTP)

Set `storage.type` and the backend block. ortus lists the backend, downloads
supported sources (GeoPackages and raster bundles), indexes, and serves them.

## AWS S3

```yaml
storage:
  type: s3
  s3:
    bucket: my-geopackages
    region: eu-central-1
    prefix: gpkg/
```

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
./ortus --storage-type=s3
```

## Azure Blob Storage

```yaml
storage:
  type: azure
  azure:
    container: geopackages
    account_name: mystorageaccount
```

## HTTP download

```yaml
storage:
  type: http
  http:
    base_url: "https://data.example.com/gpkg/"
    index_file: "index.txt"
```

To pick up sources added *after* startup, enable
[remote storage sync](sync-remote-storage.md).
