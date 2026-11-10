-- Erzeugt eine Mini-GeoPackage mit einem Quadrat um Wertheim (Gazetteer-Fixture-Punkt
-- lon 9.52316 / lat 49.76028) in der SRID :srid. Aufruf über scripts/smoke-fixtures.sh.
PRAGMA trusted_schema = 1;
SELECT load_extension(:lib);
CREATE TABLE gpkg_contents (
  table_name TEXT PRIMARY KEY, data_type TEXT, identifier TEXT, description TEXT,
  min_x DOUBLE, min_y DOUBLE, max_x DOUBLE, max_y DOUBLE, srs_id INTEGER);
CREATE TABLE gpkg_geometry_columns (
  table_name TEXT, column_name TEXT, geometry_type_name TEXT, srs_id INTEGER, z TINYINT, m TINYINT);
CREATE TABLE area (fid INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, geom BLOB);
INSERT INTO gpkg_contents VALUES ('area', 'features', 'area', 'ortus smoke fixture', :minx, :miny, :maxx, :maxy, :srid);
INSERT INTO gpkg_geometry_columns VALUES ('area', 'geom', 'POLYGON', :srid, 0, 0);
INSERT INTO area (name, geom) VALUES ('square', AsGPB(BuildMbr(:minx, :miny, :maxx, :maxy, :srid)));
