import { format } from "sql-formatter";

// One entry point, so the Go side holds no more than a function and a string.
globalThis.formatSql = (sql, language) => format(sql, { language });
