# gocog

A pure Go library for reading Cloud Optimized GeoTIFF (COG) files without any C/C++/Rust dependencies. This library provides efficient access to geospatial raster data and exposes geometry information using types from [github.com/paulmach/orb](https://github.com/paulmach/orb).

## Features

- **Pure Go Implementation** - No external C/C++/Rust dependencies
- **Cloud Optimized GeoTIFF Support** - Efficient reading of COG files from cloud storage
- **HTTP Range Request Support** - Optimized for reading specific regions without downloading entire files
- **Automatic URL/File Detection** - The `Open()` function automatically detects URLs vs file paths
- **Geospatial Metadata** - Extracts CRS, georeferencing, and projection information
- **Geometry Integration** - Uses [orb](https://github.com/paulmach/orb) types for geometry representation
- **Overview Support** - Access to multiple resolution levels (pyramids) with automatic selection
- **Tiled and Stripped Images** - Efficient access to both tiled and stripped TIFF formats
- **Compression Support** - Supports multiple compression formats: None, LZW, Deflate/ZIP, and JPEG
- **Multiple Data Types** - Supports various pixel data types (8/16/32-bit integers, floats, signed/unsigned)
- **Map Tile Support** - Read standard map tiles (XYZ tiles) from COG files with automatic resampling
- **Pixel Space Windows** - Read rectangular regions in pixel coordinates with automatic overview selection
- **Optimized Metadata Reading** - Efficient metadata extraction using single-buffer reads and lazy loading
- **High Performance** - Buffer pooling, parallel tile decompression, flat memory layout, and HTTP read-ahead buffering

## Installation

```bash
go get github.com/tingold/gocog
```

## Quick Start

```go
package main

import (
    "fmt"
    "github.com/tingold/gocog"
    "github.com/paulmach/orb"
    "github.com/paulmach/orb/maptile"
)

func main() {
    // Open a COG file (automatically detects URLs vs file paths)
    cog, err := gocog.Open("example.cog.tif", nil)
    if err != nil {
        panic(err)
    }
    
    // Get the bounding box as an orb.Bound
    bounds := cog.Bounds()
    fmt.Printf("Bounds: %+v\n", bounds)
    
    // Get the CRS
    crs := cog.CRS()
    fmt.Printf("CRS: %s\n", crs)
    
    // Read a specific region
    region := orb.Bound{
        Min: orb.Point{bounds.Min[0], bounds.Min[1]},
        Max: orb.Point{bounds.Min[0] + 0.1, bounds.Min[1] + 0.1},
    }
    
    data, err := cog.ReadRegion(region, 0) // 0 = highest resolution
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Read region: %dx%d pixels\n", data.Width, data.Height)
    
    // Read a map tile (XYZ tile format)
    // Create a tile at zoom level 10 using the center point of the bounds
    centerPoint := orb.Point{
        (bounds.Min[0] + bounds.Max[0]) / 2,
        (bounds.Min[1] + bounds.Max[1]) / 2,
    }
    zoom := maptile.Zoom(10)
    tile := maptile.At(centerPoint, zoom)
    
    tileData, err := cog.ReadTile(tile) // Defaults to 256x256 pixels
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Read tile: %dx%d pixels, %d bands\n", 
        tileData.Width, tileData.Height, tileData.Bands)
    
    // Read a tile with custom size (512x512)
    customTileData, err := cog.ReadTile(tile, 512)
    if err != nil {
        panic(err)
    }
    
    fmt.Printf("Read custom size tile: %dx%d pixels\n", 
        customTileData.Width, customTileData.Height)
}
```

## API Overview

### Reading COG Files

- `Open(pathOrURL string, client *fasthttp.Client) (*COG, error)` - Open a COG from a file path or URL (automatically detects which). Validates the file can be opened and reads metadata only.
- `Read(io.ReadSeeker) (*COG, error)` - Read a COG from an io.ReadSeeker (file or HTTP range reader)
- `ReadFromURL(url string, client *fasthttp.Client) (*COG, error)` - Read a COG from a URL using fasthttp

### Accessing Metadata

- `Bounds() orb.Bound` - Get the geographic bounding box
- `CRS() string` - Get the Coordinate Reference System (e.g., "EPSG:4326")
- `Width() int` - Get image width in pixels
- `Height() int` - Get image height in pixels
- `BandCount() int` - Get number of bands
- `DataType() DataType` - Get pixel data type
- `OverviewCount() int` - Get the number of overview levels available
- `GetOverview(level int) *GeoTIFFMetadata` - Get metadata for a specific overview level (0 = highest resolution overview)

### Reading Data

- `ReadRegion(bound orb.Bound, overview int) (*RasterData, error)` - Read a geographic region from the specified overview level (0 = main image)
- `ReadWindow(rect Rectangle) (*RasterData, error)` - Read a window (rectangle) in pixel space. Automatically selects the appropriate overview level to minimize data transfer while maintaining reasonable resolution.
- `ReadTile(tile maptile.Tile, tileSize ...int) (*RasterData, error)` - Read a map tile from the COG. Supports EPSG:4326 and EPSG:3857 CRS. The tile will be resampled to the specified size (defaults to 256x256 if not provided).

### Types

- `Rectangle` - Represents a rectangle in pixel space with fields: `X`, `Y`, `Width`, `Height`
- `RasterData` - Contains raster data with fields:
  - `Data []uint64` - Flat array in band-interleaved-by-pixel (BIP) format
  - `Width`, `Height`, `Bands int` - Dimensions
  - `Bounds orb.Bound` - Geographic bounds
  - `At(band, x, y int) uint64` - Get pixel value at coordinates
  - `Set(band, x, y int, value uint64)` - Set pixel value
  - `AtUnchecked(band, x, y int) uint64` - Fast access without bounds checking
  - `GetBand(band int) []uint64` - Extract single band as slice
  - `GetPixel(x, y int) []uint64` - Get all band values for a pixel
- `DataType` - Represents pixel data types: `DTByte`, `DTSByte`, `DTSShort`, `DTSShortS`, `DTSLong`, `DTSLongS`, `DTFloat`, `DTDouble`, `DTRational`, `DTSRational`, `DTASCII`, `DTUndefined`

### Compression Support

The library supports reading COG files with the following compression formats:
- **None** (uncompressed)
- **LZW** (TIFF compression)
- **Deflate/ZIP** (ZIP compression)
- **JPEG** (JPEG compression for tiles/strips)

Compression is automatically detected and handled transparently when reading pixel data.

## Performance

The library is optimized for high performance with the following features:

### gocog vs Rasterio Comparison

gocog is a pure Go implementation that avoids the overhead of Python's interpreter and GDAL's C library infrastructure. When comparing metadata retrieval operations against rasterio (Python library using GDAL):

| File | gocog | rasterio | Speedup |
|------|-------|----------|---------|
| TCI.tif (local) | 319µs | 21.774ms | 68.27x faster |
| B12.tif (local) | 199µs | 22.326ms | 112.44x faster |
| TCI.tif (remote) | 548.399ms | 963.965ms | 1.76x faster |

Run the rasterio comparison tests yourself:

```bash
# Quick comparison test (local files)
go test -v -run "TestCompareRasterioVsGoCOG$"

# URL comparison test (remote files)
go test -v -run "TestCompareRasterioVsGoCOG_URL$"

# Summary table (includes local and remote)
go test -v -run "TestPrintRasterioSummary"

# Full benchmarks (requires Python with rasterio installed)
go test -bench="BenchmarkGoCOG_OpenInfo|BenchmarkGoCOG_OpenInfo_URL"

# Run comprehensive comparison script (runs both Go and Python benchmarks)
./compare_benchmarks.sh
```

**Note:** The comparison script requires Python with rasterio installed. Install dependencies with:
```bash
pip install -r requirements.txt
# or with uv:
uv pip install -r requirements.txt
```

### gocog vs GDAL Comparison

One of the key advantages of gocog is its pure Go implementation, which avoids the overhead of loading GDAL's C library infrastructure. When comparing metadata retrieval operations against GDAL's `gdalinfo` utility:

| File | gocog | gdalinfo | Speedup |
|------|-------|----------|---------|
| TCI.tif (15360x15872, 3 bands) | ~107µs | ~700ms | **~6,500x faster** |
| B12.tif (7680x8192, 1 band) | ~90µs | ~690ms | **~7,500x faster** |

Run the GDAL comparison tests yourself:

```bash
# Quick comparison test
go test -v -run "TestCompareGDALvsGoCOG$"

# Summary table
go test -v -run "TestPrintSummary"

# Full benchmarks (requires gdalinfo in PATH)
go test -bench="BenchmarkGoCOG_OpenInfo|BenchmarkGDALInfo"
```

### Optimizations

- **Flat Memory Layout** - Raster data uses a flat `[]uint64` array instead of nested slices, reducing allocations from 771 to 1 and improving cache locality
- **Buffer Pooling** - Reusable buffer pools via `sync.Pool` reduce GC pressure (620x faster buffer allocation)
- **Parallel Tile Decompression** - Multi-core systems benefit from parallel decompression of compressed tiles
- **Strip Caching** - Decompressed strips are cached to avoid redundant decompression
- **HTTP Read-Ahead** - Sequential HTTP reads use read-ahead buffering for improved throughput

### Benchmark Results

Run benchmarks with:

```bash
# All benchmarks
go test -bench=. -benchmem

# Rasterio comparison benchmarks (local and remote)
go test -bench="BenchmarkGoCOG_OpenInfo|BenchmarkGoCOG_OpenInfo_URL" -benchtime=3s

# GDAL comparison benchmarks
go test -bench="BenchmarkGoCOG_OpenInfo|BenchmarkGDALInfo" -benchtime=3s

# Comprehensive comparison (Go + Python benchmarks)
./compare_benchmarks.sh
```

Example results on Intel Core i9-9980HK (16 cores):

| Benchmark | Time | Allocations | Notes |
|-----------|------|-------------|-------|
| DecodeBytes 256x256x3 | 644µs | 1 alloc | Single allocation for flat array |
| DecodeBytes 512x512x3 | 2.7ms | 1 alloc | Scales linearly with pixels |
| Pixel Access (sequential) | 226µs | 0 allocs | Zero-copy access |
| Pixel Access (direct) | 54µs | 0 allocs | Fastest via flat array iteration |
| Buffer Alloc (new) | 27.9µs | 1 alloc | Standard allocation |
| Buffer Alloc (pooled) | 45ns | 0 allocs | **620x faster** with pooling |
| Parallel Tile Processing | 241µs | 34 allocs | 16 tiles in parallel |
| Sequential Tile Processing | 898µs | 0 allocs | 16 tiles sequential |
| COG Open (local file) | 104µs | 265 allocs | Metadata-only read |
| COG ReadWindow 256x256 | 4.2ms | 44 allocs | Small window read |
| COG ReadWindow 1024x1024 | 17.3ms | 159 allocs | Large window read |

### Profiling

For detailed profiling:

```bash
# CPU profiling
go test -bench=BenchmarkCOG_ReadWindow -cpuprofile=cpu.prof
go tool pprof cpu.prof

# Memory profiling
go test -bench=BenchmarkCOG_ReadWindow -memprofile=mem.prof
go tool pprof mem.prof

# Trace analysis
go test -bench=BenchmarkCOG_ReadWindow -trace=trace.out
go tool trace trace.out
```

## License

MIT

