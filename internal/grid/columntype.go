package grid

import (
	"regexp"
	"strings"
)

// Kind is the colour bucket a result-grid value wears, decided by its
// column's declared type.
//
// Both drivers spell the same idea differently: Postgres with the catalog
// name (`integer`, `timestamp with time zone`, `integer[]`), MySQL with the
// upper-case driver name (`BIGINT`, `UNSIGNED INT`, `TIMESTAMP`). Both land
// here and collapse onto one small palette, so a cell's colour never depends
// on which driver produced it.
//
// An unrecognised name stays KindUnknown and is left on the inherited
// foreground: a wrong colour on a bit column reads as a claim about the
// data, and neutral is the one answer that never lies. Which colour each
// kind gets is the view's business; recall's CSS class names are not ported.
type Kind string

const (
	KindUnknown    Kind = "unknown"
	KindInteger    Kind = "integer"
	KindNumeric    Kind = "numeric"
	KindString     Kind = "string"
	KindBoolean    Kind = "boolean"
	KindTemporal   Kind = "temporal"
	KindStructured Kind = "structured"
	KindIdentifier Kind = "identifier"
	KindBinary     Kind = "binary"
	KindSpatial    Kind = "spatial"
)

// Base type name (lower-cased, parameters and attributes stripped) to
// bucket.
var baseKinds = map[string]Kind{

	// Postgres integer family, by declared name and by `pg_typeof` alias.
	"smallint":    KindInteger,
	"integer":     KindInteger,
	"bigint":      KindInteger,
	"serial":      KindInteger,
	"smallserial": KindInteger,
	"bigserial":   KindInteger,
	"int2":        KindInteger,
	"int4":        KindInteger,
	"int8":        KindInteger,
	// MySQL integer family.
	"tinyint":   KindInteger,
	"mediumint": KindInteger,
	"int":       KindInteger,
	"year":      KindInteger,

	"numeric":          KindNumeric,
	"decimal":          KindNumeric,
	"dec":              KindNumeric,
	"real":             KindNumeric,
	"double":           KindNumeric,
	"double precision": KindNumeric,
	"float":            KindNumeric,
	"float4":           KindNumeric,
	"float8":           KindNumeric,
	"money":            KindNumeric,
	"smallmoney":       KindNumeric,
	// The value-derived name `columnTypeName` gives a column the probe could
	// not type. A JS number is the closest thing to a numeric column.
	"number": KindNumeric,

	"char":              KindString,
	"bpchar":            KindString,
	"character":         KindString,
	"character varying": KindString,
	"varchar":           KindString,
	"varchar2":          KindString,
	"nvarchar":          KindString,
	"nchar":             KindString,
	"text":              KindString,
	"tinytext":          KindString,
	"mediumtext":        KindString,
	"longtext":          KindString,
	"clob":              KindString,
	"nclob":             KindString,
	"name":              KindString,
	"citext":            KindString,
	"enum":              KindString,
	"set":               KindString,
	"string":            KindString,

	"bool":    KindBoolean,
	"boolean": KindBoolean,

	"date":                        KindTemporal,
	"time":                        KindTemporal,
	"timetz":                      KindTemporal,
	"timestamp":                   KindTemporal,
	"timestamptz":                 KindTemporal,
	"timestamp with time zone":    KindTemporal,
	"timestamp without time zone": KindTemporal,
	"time with time zone":         KindTemporal,
	"time without time zone":      KindTemporal,
	"datetime":                    KindTemporal,
	"datetime2":                   KindTemporal,
	"smalldatetime":               KindTemporal,
	"interval":                    KindTemporal,

	"json":   KindStructured,
	"jsonb":  KindStructured,
	"xml":    KindStructured,
	"hstore": KindStructured,
	"array":  KindStructured,

	"uuid":             KindIdentifier,
	"uniqueidentifier": KindIdentifier,
	"rowid":            KindIdentifier,

	"bytea":      KindBinary,
	"blob":       KindBinary,
	"tinyblob":   KindBinary,
	"mediumblob": KindBinary,
	"longblob":   KindBinary,
	"binary":     KindBinary,
	"varbinary":  KindBinary,
	"image":      KindBinary,
	"bytes":      KindBinary,
	// A bit string is read as bytes, not as a truth value: MySQL's `bit(1)` is
	// the one exception and it arrives as `number` from the value fallback.
	"bit":         KindBinary,
	"bit varying": KindBinary,

	"geometry":           KindSpatial,
	"geography":          KindSpatial,
	"point":              KindSpatial,
	"linestring":         KindSpatial,
	"polygon":            KindSpatial,
	"multipoint":         KindSpatial,
	"multilinestring":    KindSpatial,
	"multipolygon":       KindSpatial,
	"geometrycollection": KindSpatial,
	// MySQL `DatabaseTypeName` spellings recall never saw (it had value-derived
	// names there), beyond those above. The driver reports TEXT-family and
	// BLOB-family by their size names, which are listed already.
	"fixed":          KindNumeric,
	"geomcollection": KindSpatial,
}

var (
	arrayWrapper = regexp.MustCompile(`^array\s*\((.*)\)$`)
	// MySQL column attributes carry no semantics for a colour: `int
	// unsigned` is an integer.
	columnAttrs = regexp.MustCompile(`\bunsigned\b|\bzerofill\b`)
)

// unwrap splits a type name into its base name and whether it names an
// array.
//
// Arrays are worth their own pass because Postgres exposes the same array
// three ways: `int[]` when the value was cast, `_int4` through the catalog,
// and `array(...)` when a parameter declared it. All three read as one JSON
// value in the grid, so all three belong to KindStructured rather than to
// the bucket of their element type.
func unwrap(dataType string) (base string, array bool) {
	text := strings.Join(strings.Fields(strings.ToLower(dataType)), " ")
	for {
		if strings.HasSuffix(text, "[]") {
			array = true
			text = strings.TrimSpace(strings.TrimSuffix(text, "[]"))
			continue
		}
		if m := arrayWrapper.FindStringSubmatch(text); m != nil {
			array = true
			text = strings.TrimSpace(m[1])
			continue
		}
		break
	}
	// The catalog convention for an array type name (`_int4`, `_text`). The
	// length guard keeps a bare `_` out of it.
	if !array && len(text) > 1 && text[0] == '_' {
		array = true
		text = text[1:]
	}
	// Parameters carry no semantics either: `numeric(20,6)` is a numeric.
	if i := strings.IndexByte(text, '('); i > 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = columnAttrs.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " "), array
}

// ResolveKind is the colour bucket a declared type name belongs to, or
// KindUnknown.
func ResolveKind(dataType string) Kind {
	base, array := unwrap(dataType)
	if base == "" {
		return KindUnknown
	}
	if array {
		return KindStructured
	}
	if k, ok := baseKinds[base]; ok {
		return k
	}
	return KindUnknown
}
