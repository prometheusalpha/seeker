package sqltext

import (
	"strings"
	"sync"
)

// Words a bare identifier cannot be spelled as without quoting. Recall's
// editor lexicon (sqlLexicon.ts), which is the editor's highlighting list and
// not the servers' reserved-word tables: it errs towards quoting, which is
// harmless in a WHERE fragment.
const (
	postgresKeywords = "abort absolute access action add admin after aggregate all also alter always analyse analyze and any array as asc assertion assignment at attach attribute authorization backward before begin between both by cache call cascade cascaded case cast catalog chain characteristics check checkpoint class close cluster coalesce collate collation collation_catalog column columns comment comments commit committed concurrently configuration conflict connection constraints content continue conversion copy cost create cross csv cube current current_catalog current_date current_role current_schema current_time current_timestamp current_user cursor cycle data database day deallocate declare default defaults deferrable deferred definer delete delimiter delimiters depends desc detach dictionary disable discard distinct do document domain double drop each else enable encoding encrypted end enum escape event except exclude excluding exclusive execute exists explain expression extension external extract false family fetch filter first following for force foreign forward freeze from full function functions generated global grant granted greatest group grouping groups handler having header hold hour identity if ignore ilike immediate immutable implicit import in include including increment index indexes inherit inherits initially inline inner inout input insensitive insert instead invoker isolation key label language large last lateral leakproof least left level like limit listen load local localtime localtimestamp location lock locked logged mapping match materialized maxvalue method minute minvalue mode month move name names national natural nchar new next no none not nothing notify notnull nowait null nullif nulls object of off oids old on only operator option options or order ordinality out outer over overlaps owned owner parser partial partition passing password plans policy preceding precision prepare prepared preserve primary prior privileges procedural procedure program quote range read real reassign recheck recursive ref references referencing refresh reindex relative release rename repeatable replace replica reset restart restrict return returns revoke role rollback rollup routine row rows rule savepoint scale schema scroll search second security select sequence sequences serializable server session set setof sets share show simple skip snapshot sql stable standalone start statement statistics stdin stdout storage strict strip structure subscription support sysid system table tables tablesample tablespace temp template temporary text then ties to transaction transform treat trigger trim true truncate trusted type types unbounded uncommitted unencrypted unknown unlisten unlogged until update user using vacuum valid validate validator value values varying verbose version view views volatile when where whitespace window with within without work wrapper write xml year yes zone"
	mysqlKeywords    = "accessible add all alter analyze and as asc asensitive before between bigint binary blob both by call cascade case change char character check collate collation column condition constraint continue convert create cross current_date current_time current_timestamp current_user cursor database databases day_hour day_microsecond day_minute day_second dec decimal declare default delayed delete desc describe deterministic distinct distinctrow div double drop dual each else elseif enclosed escaped exists exit explain false fetch float float4 float8 for force foreign from fulltext grant group having high_priority hour_microsecond hour_minute hour_second if ignore in index infile inner inout insensitive insert int int1 int2 int3 int4 int8 integer interval into io_after_gtids io_before_gtids is iterate join key keys kill leading leave left like limit linear lines load localtime localtimestamp lock long longblob longtext loop low_priority master_bind match maxvalue mediumblob mediumint mediumtext middleint minute_microsecond minute_second mod modifies natural not no_write_to_binlog null numeric on optimize optimizer_costs option optionally or order out outer outfile precision primary procedure purge range read reads read_write real references regexp release rename repeat replace require restrict return revoke right rlike schema schemas second_microsecond select sensitive separator set show smallint spatial specific sql sqlexception sqlstate sqlwarning sql_big_result sql_calc_found_rows sql_small_result ssl starting stored straight_join table tables terminated then tinyblob tinyint tinytext to trailing trigger true undo union unique unlock unsigned update usage use using utc_date utc_time utc_timestamp values varbinary varchar varcharacter varying when where while with write xor year_month zerofill"
)

var reserved = [2]func() map[string]bool{
	Postgres: sync.OnceValue(func() map[string]bool { return wordSet(postgresKeywords) }),
	MySQL:    sync.OnceValue(func() map[string]bool { return wordSet(mysqlKeywords) }),
}

func wordSet(words string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(words) {
		set[strings.ToLower(w)] = true
	}
	return set
}

// Keywords lists the words the editor offers for the dialect, lower case as
// recall's lexicon spells them.
func Keywords(d Dialect) []string {
	if d == MySQL {
		return strings.Fields(mysqlKeywords)
	}
	return strings.Fields(postgresKeywords)
}
