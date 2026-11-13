// Run by golden.sh: formats each case with sql-formatter in bun, as recall's
// formatSql.ts does, and writes the pairs the Go test compares against.
import { format } from "sql-formatter";

const cases = [
  ["postgresql", "select a::int, b::text from t join u on u.id=t.id where x in (1,2) order by a desc limit 5"],
  ["postgresql", "WITH x AS (SELECT id FROM t WHERE n > 1) SELECT * FROM x LEFT JOIN y USING (id)"],
  ["postgresql", "insert into t (a,b) values (1,'it''s'),(2,$$x;y$$) returning id"],
  ["postgresql", "select * from t where j->>'k' = 'v' and a = any($1) -- trailing\n;select 2"],
  ["mysql", "select `a`, b from t join u on t.id=u.id where x like '%a%' limit 5, 10"],
  ["mysql", "UPDATE t SET a = 1, b = IFNULL(b, 0) WHERE id IN (SELECT id FROM u) # note"],
  ["mysql", "create table t (id int primary key auto_increment, name varchar(20) not null)"],
  ["postgresql", "select\n\n  1"],
];
const out = cases.map(([language, sql]) => ({ language, sql, want: format(sql, { language }) }));
await Bun.write(process.argv[2], JSON.stringify(out, null, 2) + "\n");
