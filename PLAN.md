# Seeker: port recall sang mygo native UI

Nguồn: `../recall` (Electrobun + Bun + Vue, ~33k dòng, 16 file test bun). Đích: Go + fork `../mygo` (nhánh `main`, mới hơn v0.9.0), package `ui` vẽ native, không webview.

Đọc file này trước khi làm bất cứ phase nào. Mỗi phase có: phạm vi, file nguồn bên recall, what should know, gotchas, xong khi.

---

## 1. Quyết định đã chốt

1. Port đúng hành vi hiện tại của recall trước. Mục chưa tick trong `../recall/TODO.md` thành backlog (mục 6), làm sau parity.
2. Redis port ở M7.
3. App DB dùng `plugins/sqlite` của mygo, bọc sau `internal/store` để đổi được (`modernc.org/sqlite` là phương án dự phòng).
4. Format SQL: goja + bundle `sql-formatter` (giữ y hệt recall: chỉ set dialect, giữ case, indent 2). GoSQLX đã thử và loại: viết lại SQL (`::` → `CAST`, `JOIN` → `INNER JOIN`, `LIMIT a,b` → `OFFSET`), tự viết hoa keyword, không wrap dòng dài, không thụt lề subquery/CTE.
5. Find in files: bundle `rg` như recall.
6. Dùng lại dữ liệu recall: import connections, bookmarks, pinned tables từ `recall.db` một lần; password keychain đọc từ `recall.connection.<id>` rồi move sang `seeker.connection.<id>` (xoá item của recall sau khi ghi xong; macOS hỏi quyền một lần; sau đó dùng item của seeker).
7. Tên hiển thị `Seeker` (`mygo.json` `name`, title cửa sổ). Module Go là `seeker`.
8. Layout island: bo 8px, khe 6px, title bar trong suốt trên nền canvas. Đã duyệt.
9. Theme có setting kiểu Zed: Appearance System/Light/Dark + theme cho sáng + theme cho tối + accent. Default Light/Dark + 4 flavor Catppuccin (palette v1.8.0), 14 accent.
10. mygo dùng fork `../mygo` qua `replace`. Ảo hoá cột và cột ghim của `ui.Table` là **option opt-in**, không hard code, không bắt buộc.
11. Thứ tự: M1 trước, việc trong fork (F1, F2) sau, ngay trước phase cần nó.
12. Identifier `io.starchaser.seeker` (`mygo.json`). `PathUserData` đi theo nó: không đổi nữa sau khi có dữ liệu thật.
13. Git: repo `seeker`, remote `origin` = `git@github.com:prometheusalpha/seeker.git`.
14. Script chỉ có comment (`select 1;\n-- done`): giữ cách mới, `sqltext.Split` bỏ đoạn không có code (chốt 2026-11-13).
15. Docker test: OrbStack + `../recall/compose.yaml`, agent tự bật khi cần test DB thật.
16. Session chạy SQL của user: **mỗi tab một session** (key `connection, database, tab`), không dùng chung theo database: giữ `SET`/temp table/`BEGIN` của tab, và hai tab cùng database chạy song song. Session catalog (sidebar): tối đa 3 mỗi (connection, database); MySQL dùng session của profile cho mọi database.

## 2. Quyết định còn mở

(không có)

---

## 3. Kiến trúc hiện tại

```
main.go                cửa sổ 1280×820, TitleBarHidden, StateKey "main"
internal/
  model/      Profile, Bookmark, PinnedTable (dùng chung store/driver/view)
  store/      app SQLite (plugin), import recall
  secret/     mygo.Secrets, service seeker.connection.<id>, move từ recall.connection.<id> khi chưa có
  sqltext/    Split/At (đã gộp 2 splitter của recall)
  theme/      Settings → ui.Theme + Islands + editor.Theme; Catppuccin
  icons/      lucide 0.470.0 (svg/*.svg, go:embed)
  view/       layout island (mock), settings dialog
  testenv/    helper cho test (sqlite native lib)
  driver/     Conn = một session; postgres (pgx v5), mysql (go-sql-driver v1.10); rowUpdate
  pool/       session catalog + session theo tab, idle probe, thay session chết, OnLost
  grid/       formatCell, kind, width, columnWindow/Order, cellSum, sqlInsert, pendingEdits
  explain/    parse plan PG/MySQL + layout canvas
  sqlfmt/     goja + sql-formatter 15.9.0 (bundle bằng gen.sh)
  rank/       tableRank + fuzzy (Quick Open, gợi ý cột)
  # sắp có:
  files/      sqlFiles, findInFiles (rg)
  redis/      go-redis + commandSafety
```

- `view` lớn dần sẽ tách: `view/sidebar`, `view/workspace`, `view/grid`… Giữ mỗi file < 1000 dòng.
- State của app nằm trong struct của `view` hoặc package `app` riêng khi đủ lớn. Không giữ `ui.Context` hay `ui.Element` trong state.

## 4. Kiến thức chung (đọc trước mọi phase)

### mygo native UI
- View chạy lại nhiều lần mỗi event. Không làm side effect trong lúc build; I/O chạy trong goroutine, đẩy kết quả về bằng `Window.Update` (bất đồng bộ). Kết quả cũ phải bị bỏ khi nguồn đã đổi (tab đổi, query mới).
- `ui.Element` hết hạn sau mỗi lần build. Không lưu element, không lưu `*ui.Context` vào state hay goroutine; dùng `c.Services()` cho callback lâu dài, `ui.Handle` + `.Bind` cho identity.
- Trong vòng lặp phải `c.Key(...)` để widget có identity ổn định. Trùng key: Tester panic, app chỉ log.
- Giá trị bind (`*string`, `*int`) cập nhật sau khi build; `Changed()` báo ở lần build sau.
- `c.Theme()` **trước** `c.SetTheme` là theme hệ thống của frame đó; `SetTheme` chỉ có hiệu lực trong frame. Đọc `Dark` hệ thống ở đầu `View`.
- Hàng được chọn (List/Outline/Table) tô nền accent và chữ `AccentText`. Mọi màu tự gán (icon, chữ phụ) phải đổi theo, dùng `ink()` trong `view`. Lỗi này đã xảy ra một lần.
- `Split` vẽ divider 1px màu `t.Border`. `gapSplit` bọc theme để divider trùng màu canvas, panel lấy lại theme gốc qua `ui.Themed`.
- `ui.Table` cao dòng `Space(8)`. Grid dày hơn bằng `ui.Themed` với `Spacing = 3`.
- `Outline` chỉ gọi `children` cho item đang mở → lazy load được; `Tree` build hết, không dùng cho sidebar.
- Tester: `Scroll(x, y, dx, dy)` dương là cuộn sang phải/xuống. Tester vẽ bằng CPU, số đo thời gian chỉ để so sánh tương đối.

### Fork mygo
- `go.mod` có `replace github.com/egoist/mygo => ../mygo`. Sửa fork thì test trong fork theo style repo đó, chỉ commit khi user bảo; push user tự làm.
- Plugin sqlite với fork local: runtime bỏ qua bản build publish (`native/lib_ffi.go` `published()` chỉ nhận module trong module cache) và đòi Zig. Test dùng `testenv.SQLite(t)`: tải bản publish (kiểm sha256) vào `~/Library/Caches/mygo/natives/<sha>/`, set `MYGO_NATIVE_SQLITECORE`. Lần đầu ~30s. `mygo dev`/`mygo build` không bị (CLI dùng bản publish khi `sourceHash` khớp, không có Zig).
- Máy không có Zig. Đừng chạy `mygo native build`.

### Quy trình (kế thừa `../recall/AGENTS.md`)
- Lệnh kiểm: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go tool mygo vet . && CGO_ENABLED=0 go test ./...`.
- Xanh không có nghĩa là bug đã hết. Khẳng định nguyên nhân bug phải kèm dòng code hoặc số liệu chạy thật. Đừng tin báo cáo "xong" của subagent, tự chạy lại.
- Sửa xong báo user test trong app (`go tool mygo dev`) rồi mới tick.
- Comment ghi quyết định, không TLDR; ngắn (≤ 5–10 dòng); không hard-wrap markdown.
- File < 1000 dòng.
- Commit: không Co-Author; chỉ commit khi user bảo.
- Ưu tiên lưu SQLite (`store.SetSetting`) hơn file rời.
- Setup để test (file tạm, ảnh chụp) làm xong phải xoá.
- Kiểm tra layout: viết test tạm render `tt.Image()` ra PNG rồi xem, xong xoá.

---

## 5. Các phase

### M0: Spike ✅ (2026-11-12)

Kết quả:
- `ui.Table` (100k dòng, cột 140px, cửa sổ 1200×800): chi phí tăng tuyến tính theo số cột. Cuộn dọc 300 DIP: 3.4ms (10 cột), 7ms (25), 14ms (50), 30ms (100). Frame đứng yên: 0.76 / 1.5 / 3.2 / 8ms. Nguyên nhân: `table.go` closure `cells` build mọi cột của mỗi dòng.
- Cột `RowHeader` + `Fixed` trôi khi cuộn ngang. `Sticky().Start(0)` gắn lên **text trong ô** không ghim được vì sticky không ra khỏi content box của parent (ô). Gắn lên **chính ô** (con của row) thì có thể được, xử lý ở F1.
- Chọn ô, Shift+mũi tên mở rộng vùng: chạy đúng.
- Editor: `editor.State` chỉ lộ `LineTop`, `ScrollY`, `ViewportHeight`, `VisibleLines`; không có `scrollX`, gutter (`textX`), toạ độ theo offset. Xử lý ở F2.
- Chưa spike: `Outline` load con async (rủi ro thấp, kiểm ở M2).

### Layout ✅

Shell island bằng dữ liệu giả, theme setting + Catppuccin, sửa contrast hàng được chọn. Setting chưa lưu (chờ store được nối ở M2).

### M1: Core (logic, chưa UI) ✅ (2026-11-13)

Phạm vi:
- [x] `internal/store`: schema y hệt recall + bảng `settings`; connections, bookmarks, pins, settings; `ImportRecall` một lần. Đã thử với `recall.db` thật (chỉ đọc, vào file tạm): khớp `sqlite3`.
- [x] `internal/secret`.
- [x] `internal/sqltext` `Split`/`At`.
- [x] `internal/driver`: interface + postgres (pgx v5) + mysql (go-sql-driver/mysql). Introspection: databases, schemas, tables(filter), columns, indexes, triggers, foreign keys, table DDL. Execute, explain.
- [x] `internal/pool`.
- [x] `internal/grid` (logic thuần): `formatCell`, `gridColumnType`, `gridColumnWidth`, `columnWindow`, `columnOrder`, `cellSum`, `sqlInsert`, `pendingEdits`, `rowUpdate`.
- [x] `internal/explain`: chuẩn hoá plan JSON PG/MySQL → `ExplainPlan`; layout canvas (`lib/explainCanvas.ts`).
- [x] `internal/sqlfmt`.
- [x] Port test bun: `appDbSchema` (bỏ, schema mới không có legacy), `clauseSuggestion`, `columnOrder`, `columnWindow`, `formatCell`, `connectionPoolReplacement`, `mysqlPublicKeyRetrieval`, `mysqlQueryDatabase`, `rowUpdate`, `tableRank`. `postgresTextFidelity` đổi thành test giữ microsecond qua driver Go.

Nguồn recall: `src/bun/driver.ts`, `drivers/postgres.ts` (819 dòng), `drivers/mysql.ts` (719), `connectionPool.ts`, `handlers.ts` (execute/cancel ~dòng 108–111, 160–266, 556–610), `rowUpdate.ts`, `src/mainview/lib/*`, `src/shared/types.ts`, `tests/*`.

What should know:
- Interface `Driver` của recall (`driver.ts:25`): `buildOptions`, `ping` (version), `quoteIdent`, `qualify(db, schema, table)`, `databaseScoped` (PG true, MySQL false), `listSchemas` (MySQL trả rỗng), `listDatabases(filter)`, `listTables/Columns/Indexes/Triggers/ForeignKeys`, `tableDdl`, `explain`. Bỏ `columnTypes`, `textFidelitySql`, `wrapForColumnProbe` (Bun không có metadata kết quả; pgx `FieldDescriptions` và `sql.Rows.ColumnTypes` có sẵn).
- Câu SQL introspection: port nguyên văn từ `drivers/*.ts`, kèm các rule `showSystemSchemas`, `hiddenDatabases` (database của profile không bao giờ bị ẩn).
- `parseUrlParams` (`driver.ts`): `+` là dấu cách, decode lỗi thì giữ nguyên. Phải khớp `lib/connectionDefaults.ts:readUrlParam` (dialog connection ở M2 dùng lại).
- Execute: tách bằng `sqltext.Split`, chạy tuần tự, mỗi statement một kết quả, lỗi nằm trong kết quả (không throw); báo tiến độ từng statement (recall push `queryProgress`). Cap `MAX_ROWS = 20000` mỗi statement (`stores/query.ts:9`), báo bị cắt.
- Affected rows: pgx `CommandTag.RowsAffected()`, MySQL `Result.RowsAffected()`.
- Pool (`connectionPool.ts`): key `(connectionID, database)`. PG mở thêm session cho mỗi database khi cần. MySQL: introspection dùng một session chung; SQL của user chạy trên session mở **trong** database của tab (`getQueryConnection`), vì `USE` trên handle dùng chung không an toàn. Dedupe các lần mở đang chạy (`opening`). Giữ profile tới khi disconnect để mở lại mà không cần gửi lại mật khẩu.
- Idle probe: session idle ≥ 5s và không có query đang chạy thì ping trước (cap 5s); ping lỗi thì thay session. Lý do: NAT/firewall cắt socket im lặng, request sau treo **quá 300s**. Dedupe probe đồng thời.
- Timeout: metadata 30s, execute không giới hạn (`queryTimeoutSecs` của profile: kiểm xem recall có dùng không trước khi áp).
- Hiển thị thời gian (`lib/formatCell.ts`): `timestamptz`, `timestamp with time zone` và `timestamp` **của MySQL** là instant, đổi sang `Asia/Bangkok`, hậu tố `+07`, giữ nguyên phần lẻ giây (microsecond). `timestamp` của PG là wall clock, giữ nguyên. Có test `formatCell` để port.
- `rowUpdate`: UPDATE theo PK, nhiều dòng trong một transaction. PG `$n`, MySQL `?` theo thứ tự xuất hiện trong text; quote identifier theo dialect.
- Explain: chỉ ước lượng, **không bao giờ** `ANALYZE`. PG `EXPLAIN (FORMAT JSON)`, MySQL `EXPLAIN FORMAT=JSON`. Có `alreadyExplains` (câu đã bắt đầu bằng EXPLAIN). Lỗi trả về dạng data.
- DB test: `../recall/compose.yaml`: PG 17 `127.0.0.1:55432` (postgres/postgres, db `recall_test`), MySQL 8.4 `53306` (root/mysql), Redis 7.4 `56379`. Env đặt tên `SEEKER_TEST_PG_URL`, `SEEKER_TEST_MYSQL_URL`, `SEEKER_TEST_REDIS_URL`; thiếu env thì `t.Skip` kèm thông báo rõ (test của store thì fail, xem `testenv`).

Gotchas:
- **pgx và prepared statement:** chế độ mặc định `QueryExecModeCacheStatement` cache plan; sau DDL (đổi cột) sẽ lỗi `cached plan must not change result type`. SQL của user chạy bằng `QueryExecModeSimpleProtocol`: giá trị về dạng text, giữ microsecond, không phải decode kiểu lạ (enum, range, domain…). Introspection dùng extended protocol với tham số.
- **pgx cancel:** huỷ context thì pgconn gửi CancelRequest rồi **có thể đóng connection** → mất state của session (temp table, `SET`). Sau cancel phải kiểm `conn.IsClosed()` và thay session; báo user nếu session bị reset.
- **go-sql-driver cancel:** huỷ context chỉ đóng socket phía client, **query vẫn chạy trên server**. Phải `KILL QUERY <thread_id>` từ một connection khác (lấy `CONNECTION_ID()` lúc mở session).
- **`database/sql` là pool:** `*sql.DB` tự mở/đóng connection, nên session-state (`USE`, `SET`, temp table, transaction người dùng mở bằng `BEGIN`) chỉ an toàn trên `*sql.Conn` giữ riêng. Mỗi tab/database giữ một `*sql.Conn`.
- **MySQL DSN:** `parseTime=false` để giữ text (không mất microsecond, không lệch timezone); `allowPublicKeyRetrieval=true` mặc định, user tắt được qua urlParams (`mysql.ts:70`); không có database thì bỏ hẳn dbname; `multiStatements=false` (tự tách).
- **MySQL 8.4 auth:** compose ép `mysql_native_password` vì Bun. Go driver hỗ trợ `caching_sha2_password`, nhưng qua TCP thường phải có `allowPublicKeyRetrieval` hoặc TLS. Root chỉ tạo ở lần boot đầu; đổi auth phải `docker compose down -v`.
- **Vị trí lỗi PG:** `position` là offset **ký tự** (1-based) trong câu đã gửi; editor dùng **byte**. Đổi rune → byte rồi cộng `Statement.Start`. MySQL không trả vị trí.
- **Không replay statement:** `WITH x AS (INSERT … RETURNING *) SELECT …` trông như câu đọc; chạy lại là nhân đôi dữ liệu. Mở lại session thì không tự chạy lại câu đã gửi.
- **Thay session (Go):** khi thay session chết, goroutine dọn session cũ không được xoá entry mới. So sánh con trỏ (identity), không so key (recall dính bug này: toast "Connection lost" giả).
- **Cap 20000 dòng:** ngừng đọc khi đủ nhưng vẫn phải đóng `rows`. pgx `rows.Close()` vẫn nhận nốt dữ liệu còn lại từ server: chấp nhận, hoặc cancel nếu quá lớn.
- **Bytes:** recall hiển thị binary bằng base64 (`toJsonSafe`). Kiểm tra lại phần bytes trong `formatCell.ts` trước khi chọn cách hiển thị.
- **Bỏ hẳn:** ép timezone `+05:45`, `textFidelitySql`, probe tên cột `LIMIT 0`, `toJsonSafe` cho RPC, `executions`/`cancelledExecutions` (thay bằng `context`).
- **sqlite plugin:** không `BEGIN/COMMIT` tay, chỉ `Transaction(ctx, []Statement)` theo lô; đọc rồi quyết định ghi phải làm ngoài lô; mỗi `Execute` một câu; chặn `ATTACH`.
- **sqlfmt:** chạy goja ngoài UI thread. Lỗi parse của `sql-formatter` có thể dài hàng nghìn ký tự (liệt kê mọi token hợp lệ), phải cắt trước khi hiện. Bundle JS (esbuild/bun build một file) rồi `go:embed`; ghi lại version.

Xong khi: test PG/MySQL thật xanh (introspection, execute nhiều statement, cancel, idle probe với TCP proxy nuốt packet, giữ microsecond, rowUpdate), test logic grid/explain/sqlfmt xanh.

Kết quả (đã làm, khác recall ở đâu):
- Chạy test DB: `SEEKER_TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:55432/recall_test SEEKER_TEST_MYSQL_URL=mysql://root:mysql@127.0.0.1:53306/recall_test`. Test pool với proxy đã thử tắt probe: test treo, tức là test bắt được lỗi thật.
- Cancel giữ session: PG dùng `CancelRequestContextWatcherHandler` (cắt socket sau 5s nếu server không trả lời); MySQL chạy query không gắn ctx, cancel bằng `KILL QUERY` từ connection thứ hai. `SELECT SLEEP()` bị kill trả 1 chứ không lỗi.
- Giá trị ô: `nil` / `string` (text của server) / `[]byte` (bytea, BLOB, VARBINARY…). go-sql-driver tự parse số kể cả text protocol: số nguyên in lại y nguyên, float in dạng ngắn nhất của Go (`1e+21` thay vì `1e21`).
- Kiểu cột: PG = `format_type` (giống `pg_typeof` của recall, cache theo session); MySQL = `DatabaseTypeName()` (`TIMESTAMP`, `VARCHAR`…), recall thì đoán từ giá trị.
- MySQL `TIMESTAMP`: driver nối offset của session (`+07:00`) vào text. Offset đọc một lần lúc mở session, đọc lại sau statement có chữ `time_zone`; zone đổi trong procedure thì không biết. Zone có DST thì là offset hiện tại.
- `allowPublicKeyRetrieval=false`: go-sql-driver luôn tự lấy RSA key, không có công tắc, nên đổi nghĩa thành **bắt buộc TLS verify-full** (giữ verify-ca nếu profile chọn): login không cần xin key, và chặn được MITM như ý định gốc. Server cert tự ký thì từ chối (đã test).
- `queryTimeoutSecs` recall không dùng ở backend → không áp. `connectTimeoutSecs` áp cho cả PG và MySQL (mặc định 30).
- Explain: MySQL 8.4 với `explain_json_format_version=2` để cây ở gốc document (không có `query_plan`); recall không nhận ra và vẽ một node rỗng. Seeker nhận cả ba dạng.
- `formatCell` dùng `FixedZone(+07)` thay vì `Asia/Bangkok` (ngày trước 1920 recall lệch LMT nhưng vẫn ghi `+07`). `sqlInsert` ghi binary dạng hex (`X'..'`, `'\x..'`), recall ghi chuỗi base64 → insert sai byte.
- `sqltext` có thêm `clause.go` (gợi ý WHERE/ORDER BY) và import `rank` (fuzzy).

### F1 (fork): `ui.Table` ảo hoá cột + cột ghim (trước M4) ✅ (2026-11-15)

Phạm vi:
- `ListState` hoặc `TableColumn` thêm option opt-in: ảo hoá cột (chỉ build cột trong `[scrollX, scrollX+viewW]` của frame trước, phần ngoài là khối trống bằng tổng độ rộng) và `Frozen` (ghim cột khi cuộn ngang). Mặc định tắt: hành vi cũ giữ nguyên.
- Test trong `../mygo/ui/table_test.go`; đo lại như M0 (10/25/50/100 cột) khi bật/tắt.

What should know:
- `table.go`: closure `cells` (~dòng 229) build mọi ô; header là `coreRow` với `head.followX = list` (header cuộn theo list); list có `flagScrollX`, `rowMinW = least`.
- Selection: `s.tableCols`, `s.block()`, `Cells.Anchor/Cursor` theo ID cột; `CellSelected(row, col)`.

Gotchas:
- Ảo hoá chỉ áp khi mọi cột có `Width` cố định; cột flex (Width 0) chia phần còn lại, không tính trước được.
- Phím mũi tên đưa cursor tới cột chưa build: phải tính x từ tổng độ rộng rồi cuộn tới.
- Double-click vừa cột chỉ đo được ô đang hiện.
- Kéo đổi chỗ cột khi header ngoài màn hình chưa được build.
- Sticky gắn lên ô (con của row) và header phải đi cùng `followX`. Kiểm bằng test, đừng suy từ docs.
- Accessibility: cột không build vẫn phải báo đúng tổng số cột cho assistive tech nếu được.

### F2 (fork): API toạ độ cho editor (trước M3) ✅ (2026-11-13)

Phạm vi: `editor.State` thêm cách lấy toạ độ màn hình của một offset (vd. `Rect(offset)` hoặc `ScrollX()` + `TextX()`), đủ để vẽ viền 4 cạnh bao statement "tới ký tự xa nhất" (recall 7.2/9.3) bằng `DrawOver`. Test trong fork.

What should know: nội bộ có `ed.scrollX`, `ed.textX`, `ed.scrollY` (`ui/editor/editor.go`, `build.go:136` vẽ caret bằng `ed.textX+cr.X-ed.scrollX`). Soft wrap: một dòng nhiều hàng, `LineTop` có thể là ước lượng trong vài frame với dòng xa viewport.

Gotchas: fold ẩn dòng; tab và ký tự rộng; RTL. Trong lúc chờ F2, dùng `LineBackgrounds` (tô nền) cho statement đang đứng.

Kết quả: `State.Rect(off) (ui.Rect, bool)` (toạ độ caret theo byte offset, tính từ góc trên trái hộp editor, đã trừ scroll và cộng gutter; false khi dòng bị fold), `ScrollX()`, `TextX()`. Test `ui/editor/rect_test.go` (offset, fold, soft wrap, scroll). Đã commit trong fork: `a1d1d92c`.

### M2: Shell thật (kết nối, duyệt schema, tab)

Phạm vi:
- Mở store ở `PathUserData`, `ImportRecall` lần đầu, lưu/đọc settings (theme, kích thước panel, panel ẩn/hiện).
- App menu (`mygo.App.SetMenu`) có Edit (copy/paste/select all), Window.
- ConnectionTree thật: connection → database → schema (PG) → table → nhóm lazy (Columns / Indexes / Foreign keys / Triggers); filter có debounce; pins ở trên cùng; trạng thái connected; context menu: Reload, Cancel connect, Reconnect, Edit, Databases…, Pin/Unpin, Generate/Copy DDL.
- Dialog connection (PG/MySQL, Redis để M7): fields, URL params / SSL, timeout, test connection, lưu mật khẩu.
- Dialog Database visibility (`hiddenDatabases`).
- TabStrip thật: pinned section + separator, kéo đổi chỗ, đóng / đóng khác / đóng hết, tab bẩn đi qua hộp xác nhận, mở lại tab đã đóng; lưu tab vào store.
- Toast (`c.Toast`): connection lost, lỗi.
- Đóng cửa sổ thì đóng hết pool.

Nguồn: `App.vue`, `sidebar/ConnectionTree.vue` (1590), `TreeRow.vue`, `AppSidebar.vue`, `composables/useFlatTree`, `dialogs/ConnectionDialog.vue`, `ConnectionFormFields.vue`, `DatabaseVisibilityDialog.vue`, `lib/connectionDefaults.ts`, `stores/connections.ts`, `stores/tabs.ts`, `workspace/TabStrip.vue`, `useTabClose`, `TabCloseDialog.vue`, `src/bun/menu.ts`, `index.ts`.

What should know:
- Tab của recall: `id, title, connectionId, database, schema, table, mode (sql/table/redis; seeker lưu `query`/`table`, `redis` thêm ở M7), sql, path, savedSql, pinned` + `closed[]`, `dirtyIds`. Validate từng field khi đọc lại.
- `testConnection` trả kết quả dạng data, không throw.
- `savePassword` false: chỉ giữ mật khẩu trong phiên.
- Recall TODO Phase 2: khi mở dropdown table thì không load vội, chỉ load nhóm khi mở nhóm đó (FK và trigger load rất chậm).

Gotchas:
- **Key của Outline:** mock đang nối path bằng `/`; tên bảng/schema có thể chứa `/`. Dùng struct key (`kind, conn, db, schema, table, group, name`).
- `Outline` không có placeholder "Loading…": `children` trả node giả "Loading…" rồi thay khi `Window.Update` về; bỏ kết quả về muộn nếu node đã đóng hoặc connection đã đổi.
- localStorage của recall (tabs, snippets, shortcuts, SQL folders, theme…) nằm trong storage của WebKit, **không** import được bằng `recall.db`. Mặc định bắt đầu trống; muốn import thì phải đọc file LocalStorage của WebKit (ngoài phạm vi trừ khi user yêu cầu).
- Đọc item keychain do recall tạo: macOS hỏi quyền lần đầu, có thể block. `Secrets` chạy off-main, nhưng UI phải chịu được chờ.
- Lỗi recall Phase 1 cần tránh lặp lại: "no open connection for id" khi mở app với tab đang trỏ tới connection chưa mở → tab phải tự mở connection khi cần.

Xong khi: mở app thấy connections đã import, kết nối PG/MySQL thật, duyệt tới cột/index, đóng mở tab, khởi động lại vẫn giữ tab và theme.

Kết quả (đã làm, chờ anh test trong app rồi mới tick):
- State nằm ở package mới `internal/app` (không phải `view`), view chỉ đọc và gọi. Store mở ở `PathUserData`; lần chạy `mygo dev` đầu tiên import 4 connection thật từ `recall.db` và ghi `imported.recall`. Theme, layout và tabs lưu vào bảng `settings`, đọc lại sau khi khởi động (có test).
- Outline gọi `children` cho mọi row đang hiện để biết row có mũi tên, nên `Children` thật sẽ kết nối mọi profile ngay frame đầu. View bọc lại (`treeChildren`): row đóng trả slice rỗng, row mở mới gọi app. Không có nó thì mở app là dial hết server.
- Lọc là server-side (`listTables(filter)`), debounce 250ms, gen tăng ngay khi gõ; kết quả về muộn bị bỏ. Pin sort lên đầu danh sách anh em, không có section riêng (recall).
- Cancel connect: thêm `pool.Adopt` để goroutine connect tự đóng session nếu bị huỷ; huỷ thì cache danh sách rỗng cho row, không tự thử lại cho tới khi đóng rồi mở lại row (giống recall). Row lỗi cũng retry theo cách đó.
- Tab: dedupe path-first, partition pinned, kéo thả bằng `Drag`/`Drop`, menu Rename/Pin/Close others/Close/Close all, `⌘W`, `⌘⇧W`, `⌘⌥W`, `⌘⇧T`, `⌘⌥←/→`; hộp xác nhận tab bẩn dùng chung một dialog; stack mở lại tab đã đóng (recall không có, plan yêu cầu) giữ 20 tab.
- Dialog connection: field theo engine, chọn TLS ghi thẳng vào `urlParams` và `ssl`, test connection, lưu mật khẩu. Save đóng dialog ngay rồi mới handshake, lỗi hiện ở tooltip của row; recall giữ dialog mở và hiện lỗi trong form.
- Dialog Databases: list từ server, database của profile không ẩn được, mỗi lần gạt lưu ngay.
- Chưa làm trong M2: panel SQL files (M5), Run/Format/Explain để disabled kèm tooltip (M3/M6), vùng kết quả còn trống (M4), xoá connection (recall không có UI), đóng cửa sổ không chặn tab bẩn.
- Sửa sau review (có test trong `internal/app`, `internal/view`): edit connection MySQL không còn reset port/user/params/TLS; appearance Light/Dark được giữ sau restart; Save khoá khi password còn đang load và không ghi đè keychain bằng chuỗi rỗng khi đọc keychain fail; password của phiên ưu tiên hơn keychain khi connect; connection mới giữ `draftID` nên Test không để lại keychain item mồ côi (Cancel xoá item); `Close` flush các write đang debounce; `Reconnect` đóng session cũ trước khi connect; tab active được restore tự connect khi `Attach`.
- Sửa thêm (nhẹ): unpin trả bảng về thứ tự server; kéo tab qua đường phân cách pinned thì pin/unpin; connection lost hiện dòng lỗi thay vì tự reconnect im lặng; Discard không sống lại khi mở lại tab; Close others/all chờ hộp thoại rồi mới đóng tab sạch; kết quả Test cũ không rơi vào dialog mới.
- Test: đi hết cây trên PG thật và MySQL thật, đi hết click path trên `ui.Tester` (connect, mở database/schema/table, pin, drag tab, dialog đóng tab, dialog connection, dialog databases), cancel connect với listener treo, restart giữ theme/tab.

### M3: SQL editor ✅ (2026-11-14)

Phạm vi:
- Một `editor.State` + `Buffer` mỗi tab (undo riêng).
- Smart run `Cmd+Enter`: đang chạy → Stop; có selection → chạy selection (tách nhiều statement); không thì statement dưới caret (`sqltext.At`); không có thì cả file. Statement đang đứng: khung (F2) hoặc tô nền tạm.
- Completion: schema (bảng, cột theo bảng có trong statement), clause (WHERE/ORDER BY), snippet theo prefix, keyword theo dialect.
- Format `⌥⇧F` (sqlfmt), Explain `⌥⇧E` (dùng ở M6), lỗi trả về gắn vào dòng/cột.
- File: autosave debounce, Save As, ghi optimistic (version sha256, lệch thì từ chối và báo), datasource picker theo file (binding có giới hạn số lượng).
- Mnemonic bookmark: gán `Ctrl+Shift+<ký tự>` hoặc `Ctrl+F11` rồi gõ ký tự; nhảy `Cmd+<ký tự>` (cấp cửa sổ); glyph ở gutter (`GutterMarks`); 36 ký tự `a-z0-9`.

Nguồn: `workspace/SqlEditor.vue` (2021), `editor/SqlStatementScope.ts`, `SqlSchemaCompletionSource.ts`, `SqlCompletionSource.ts`, `lib/sqlSnippets.ts`, `sqlDialect.ts`, `sqlLexicon.ts`, `clauseSuggestion.ts`, `statementAt.ts`, `formatSql.ts`, `fileDatasource.ts`, `stores/bookmarks.ts`, `stores/query.ts`, `composables/useSchemaCatalog.ts`, `src/bun/sqlFiles.ts`.

What should know:
- `editor.State`: `Bindings` (có chord `ThenMods/ThenKey`, `NoCommand` để nhả phím cho app), `OnInput` thấy phím trước editor, `ShowCompletions(start, items)`, `GutterMarks`, `LineBackgrounds`, `SetDiagnostics(version, …)`, `Theme` (Catppuccin đã có).
- Offset editor là byte.
- Theme editor đã nối: `a.code.Theme = res.Code` (nil = theme mặc định của editor).

Gotchas:
- **`Ctrl+Shift+<số>`:** Shift làm ra ký hiệu (`!@#…`); recall đọc phím vật lý (`mnemonicForShiftKey`). Ở mygo dùng `KeyCode`/phím vật lý, không dùng text.
- **`Cmd+<ký tự>` nhảy bookmark** đụng `Cmd+W/P/R/L/F…`. Recall: phím tắt đã đăng ký thắng, phần còn lại mới là jump. Giữ đúng thứ tự đó.
- `Cmd+Enter` và các phím của app phải gỡ khỏi keymap editor (`NoCommand`), nếu không editor ăn mất.
- `LineBackgrounds`/`TextBackgrounds` không tự dời theo edit: tính lại khi `Buffer.Version` đổi.
- Vị trí lỗi PG là ký tự, không phải byte (xem M1).
- Autosave không được ghi đè file đã đổi ngoài app (sha256 version).
- `Changed`/`Submitted` báo ở frame sau; đừng đọc giá trị dẫn xuất ngay trong frame đó.

Xong khi: viết/chạy/stop/format/bookmark như recall; user test.

Kết quả (đã làm):
- Còn thiếu so với phạm vi: panel Snippets settings (làm ở M6), ký tự mnemonic trên gutter (`GutterMarks` chỉ vẽ chấm màu), panel SQL files (M5), grid (M4).
- Mỗi tab query một `editor.State` + `Buffer` (`app.Editor(id)`, tạo lười từ `Tab.SQL`); `SyncEditors` mỗi frame ghi text về tab (dirty tự suy ra), bỏ editor của tab đã đóng. Phím của editor đi qua `OnInput` (đọc `PhysicalKey`, fallback `Key`), không cần `NoCommand`.
- Smart run `⌘↵`: đang chạy → Stop; selection (bỏ qua selection chỉ có khoảng trắng); statement dưới caret; không có (caret ở dòng trống giữa hai statement) thì cả file. Nút Run chạy cả file hoặc thành Stop. Kết quả hiện từng statement khi nó về; session là session của tab (giữ `SET`), stop dùng cancel của M1.
- Khung statement bằng F2: `StatementFrame` đo từng dòng (ký tự đầu → ký tự cuối), view vẽ `Stroke` trong `DrawOver`; dòng wrap thì cạnh phải ra mép editor; không vẽ khi có selection.
- Lỗi: `SetDiagnostics` gạch chân từ offset lỗi (PG) tới hết token; MySQL không có vị trí nên gạch cả statement. Kết quả ghi "line L, column C". Diagnostics xoá khi chạy lại.
- Format `⌥⇧F`: selection → statement dưới caret → cả file; goja chạy off-thread, bỏ kết quả nếu buffer đã đổi version; một bước undo; lỗi cắt dòng đầu, ≤200 ký tự. Explain `⌥⇧E`: selection → statement → cả file nếu chỉ có đúng một statement; plan hiện dạng text thô tới khi M6 có viewer.
- Completion: bảng (sau FROM/JOIN/INTO/UPDATE/TABLE), cột theo bảng trong statement kể cả alias (`u.`), `schema.` → bảng của schema đó, CTE, snippet mặc định của recall theo prefix, keyword theo dialect. Cache bảng/cột 5 phút (lỗi 10 giây) trong `app/catalog.go`, nguội thì nạp nền và hỏi lại khi về (recall chờ tới 250 ms, ở đây không chặn phím). Tab hiện ra thì nạp trước bảng của schema và cột của tối đa 25 bảng nó nhắc tới. `sqltext/scope.go` viết lại scanner của recall bằng token (Go RE2 không có lookbehind/lookahead).
- File: autosave 1.5s, ghi guarded theo sha256 (version đọc/ghi gần nhất; tab restore từ store dùng sha của `SavedSQL`); lệch hoặc mất file thì từ chối, tab giữ dirty và tắt autosave tới khi user lưu tay, toast có nút Overwrite. `⌘S` = ghi, tab nháp thì Save As bằng hộp thoại native `mygo.Dialog.Save` (recall bắt phải mở folder trong panel Files trước). Thoát app flush autosave đang chờ.
- Datasource picker trên run bar (connection → database), gán cho cả file; binding lưu ở settings `fileBindings`, mới nhất trước, tối đa 200. `OpenFile`/`ResolveDatasource` có sẵn cho panel files (M5).
- Bookmark: `Ctrl+Shift+<ký tự>` (phím vật lý) hoặc `Ctrl+F11` rồi ký tự (Esc huỷ; text của phím đó bị nuốt), chỉ cho tab có file; mỗi dòng một mnemonic (store lấy lại từ mnemonic khác trên dòng đó). Nhảy `⌘<ký tự>` ở cấp cửa sổ, chỉ đăng ký cho mnemonic đang có và đăng ký sau các phím của app nên ⌘W thắng. Tìm lại dòng theo hash khi file bị sửa. Gutter chỉ có chấm màu (`GutterMarks` không vẽ chữ), nên chưa thấy ký tự mnemonic trên gutter.
- Không có panel Snippets settings, SQL files panel (M5), grid (M4: kết quả hiện 12 dòng đầu dạng text).
- Test: `sqltext/scope_test.go`, `app/editor_test.go` (buffer riêng mỗi tab, smart target, frame, format, completion, file guard, Save As, binding, bookmark, phím), `app/editor_live_test.go` (PG và MySQL thật: chạy, lỗi + diagnostics, SET giữ qua lần chạy, Stop, Explain, completion từ catalog thật), `view/editor_test.go` (⌘↵, ⌥⇧F, Stop, bookmark/⌘W). `go test -race` xanh cho app/view/sqltext.

### M4: Result grid + table tab ✅ (2026-11-15)

Phạm vi:
- Grid trên `ui.Table` (đã có F1): bật ảo hoá cột + cột số dòng ghim; độ rộng tự tính theo nội dung (min/max); chọn vùng (click, Shift+click, Shift+mũi tên), sum vùng chọn (bỏ ô không hợp lệ), copy TSV, Copy as INSERT, export CSV (RFC 4180) / JSON, ẩn/hiện cột, kéo đổi chỗ cột, màu chữ theo kiểu dữ liệu, `NULL` mờ, ô focus có hộp xem giá trị đầy đủ, tooltip header/cột không bị cắt.
- Sửa ô: stage → commit bằng `rowUpdate` (theo PK).
- Table tab: paging 100/500/1000/2000, `SELECT * … WHERE … ORDER BY … LIMIT/OFFSET`; filter bar WHERE / ORDER BY nhập tay có gợi ý cột, Enter chạy lại, `Cmd+L` focus WHERE; nút count (theo WHERE); `Cmd+R` chạy lại.
- Sort: `Ctrl+Shift+↑/↓` và context menu, **không** sort khi bấm tên cột (recall TODO Phase 2).
- Result view: Result | Explain; thanh trạng thái (số dòng, thời gian, bị cắt 20000).

Nguồn: `workspace/ResultGrid.vue` (2447), `QueryWorkspace.vue`, `ResultFilterBar.vue`, `ClauseSuggestInput.vue`, `composables/useGridSelection.ts`, `usePendingEdits`, `useResultFilterFocus`, `lib/gridColumnType.ts`, `gridColumnWidth.ts`, `columnWindow.ts`, `columnOrder.ts`, `cellSum.ts`, `formatCell.ts`, `sqlInsert.ts`, `pendingEdits.ts`.

What should know:
- `ui.Table` đã có: `CellSelect`, `Cells.Anchor/Cursor`, `RowHeader`, `HeaderMenu`, `CellMenu`, `HeaderCell` (tooltip), `ListState.Columns` (layout lưu được), `Changed` báo một lần mỗi thao tác kéo.
- Grid dày: `ui.Themed` với `Spacing = 3`.
- Màu chữ ô được chọn: `AccentText`; `NULL` được chọn: `AccentText` mờ.

Gotchas:
- Filter WHERE là SQL thô người dùng gõ: cố ý, nhưng phải chạy trên đúng connection/database của tab và không cho nhiều statement.
- Đổi kết quả (query mới) phải reset `ListState` (`grid = ListState{}`), nếu không selection/cột cũ dính sang.
- Ảo hoá cột cần độ rộng cố định: tính trước từ dữ liệu (monospace) trước khi dựng bảng.
- `rowUpdate` cần PK; bảng không có PK thì không cho sửa.
- Recall TODO "reload table mất thứ tự cột đã đổi": thuộc backlog, nhưng đừng tạo thêm bug này (lưu `Columns` theo bảng).

Xong khi: ngang `ResultGrid.vue`, cuộn mượt với 100 cột, user test.

Kết quả (đã làm; **chưa test tay trong app** (`go tool mygo dev`), anh test rồi mới coi là chốt):
- F1 trong fork: `ListState.VirtualColumns` (mặc định tắt) và `TableColumn.Frozen` (commit `3e104a46`, `d4dfc45e`). Grid bật ảo hoá, cột số dòng là `RowHeader`+`Fixed`+`Frozen`.
- Đo (Tester vẽ bằng CPU, chỉ để so sánh): 20000 dòng × 100 cột, cuộn dọc 2.9ms, cuộn ngang 1.9ms, frame đứng yên 0.8ms (M0, chưa ảo hoá: 30ms / 8ms).
- State: `app/gridstate.go` (`ResultGrid`: `ui.ListState` + `Selection`, cột ẩn, độ rộng, edit chờ commit, sum/copy/INSERT/CSV/JSON), `app/tabletab.go` (`TableView` mỗi table tab: WHERE/ORDER BY, trang, count, load, sort, commit), `app/gridexport.go`. View: `view/gridview.go` (bảng, ô, menu, hộp giá trị), `view/tablebar.go` (thanh lọc, phân trang), `view/results.go` (Result | Explain, chọn statement, status bar). Thuần: `grid/export.go` (TSV/CSV/JSON), `sqltext/clauseinput.go` (gợi ý WHERE/ORDER BY).
- Query tab: mỗi statement một `ResultGrid` tạo lười, bỏ khi chạy lại (selection/cột cũ không dính sang kết quả mới). Batch nhiều statement: tab `#n · thời gian` (≤8), quá thì menu. Explain vẫn là text thô (M6).
- Table tab: `SELECT * … WHERE … ORDER BY … LIMIT/OFFSET` (bỏ `OFFSET 0`), trang 100/500/1000/2000, về trang 1 khi đổi lọc/cỡ trang, trang tiếp suy từ trang đầy (không COUNT), nút Count theo WHERE (bỏ khi đổi WHERE). Chỉ một statement: WHERE có `;` thứ hai bị từ chối trước khi gửi. Lỗi lọc giữ nguyên lưới cũ và tô viền ô bị nghi. Layout cột (thứ tự, độ rộng, cột ẩn) giữ khi reload nếu cột không đổi.
- Sort: `⌃⇧↑/↓` và menu, **không** sort khi bấm tên cột. Sort ghi vào ô ORDER BY (ô đó là state duy nhất của sort). Kết quả của query không sort/sửa/INSERT được (không có bảng phía sau).
- Chọn vùng, Shift+click/mũi tên, kéo chọn, `⌘A`, kéo đổi chỗ cột: do `ui.Table` (`CellSelect`). `⌘C` copy TSV vùng chọn (đọc từ result, không từ ô đã dựng). Copy INSERT (table tab, mọi cột, giá trị y như server trả), Export CSV/JSON toàn bộ result, Columns (ẩn/hiện).
- Sửa ô (table tab có PK, PK nằm trong result): Enter/double-click/menu mở editor, Enter chờ commit (ô vàng), `Save`/`⌘S` ghi `UpdateRows` một transaction rồi đọc lại trang, lỗi thì giữ edit. `Set NULL` ở menu ô.
- `⌘R` đọc lại trang, `⌘L` vào ô WHERE, `Esc` đóng editor ô.
- Test: unit `app/gridstate_test.go`, `app/tabletab_test.go`, `grid/export_test.go`, `sqltext/clauseinput_test.go`; live PG + MySQL `app/tabletab_live_test.go` (phân trang, lọc, sort, count, lỗi lọc, commit, commit bị từ chối); live PG qua cửa sổ `view/grid_live_test.go` (chọn/sum/copy/menu/ẩn cột, ảo hoá 150 cột + cột số dòng đứng yên, hộp giá trị, kéo header, table tab đầy đủ). `-race` xanh.

Còn thiếu so với spec / ghi chú:
- Header chưa có mũi tên chiều sort (chỉ ô ORDER BY nói chiều).
- Gợi ý WHERE/ORDER BY dùng `ui.Autocomplete`: chỉ gợi ý cho từ ở **cuối** ô, chỉ khớp chứa/tiền tố (recall khớp fuzzy, gợi ý theo vị trí con trỏ).
- Hộp giá trị đầy đủ chỉ hiện khi ô bị cắt hoặc có xuống dòng (recall hiện cho mọi ô focus).
- Độ rộng cột tính bằng `grid.GridCharWidth` cố định (8px), chưa đo font thật (`grid.CharWidth(measure)` có sẵn). Font ô Menlo 12.
- Layout cột, cột ẩn, WHERE/ORDER BY, trang chỉ sống trong phiên (recall cũng không lưu); chưa vào bảng `settings`.
- Export dùng hộp thoại Save của hệ thống (recall chọn folder rồi ghi `<tab>.csv`). JSON giữ khoá trùng nếu hai cột trùng tên.
- Bảng không có PK, hoặc PK không nằm trong result: ô chỉ đọc, không báo lý do. Reload (`⌘R`, đổi lọc) bỏ edit chờ commit mà không hỏi.
- Giá trị sửa gửi dạng text; không kiểm kiểu ở client, server từ chối thì toast và giữ edit.
- Sau commit, thứ tự dòng khi không có ORDER BY là thứ tự server trả (PG đẩy dòng vừa UPDATE xuống cuối); muốn ổn định phải đặt ORDER BY.
- `Copy as INSERT` ghi cột theo thứ tự result, không theo thứ tự anh kéo; không có ở kết quả của query.
- Tooltip ô chỉ có khi text rộng hơn cột; tooltip header (tên + kiểu) chưa có test.

### M5: Files, Quick open, Find in files

Phạm vi:
- SqlFilesPanel: nhiều folder, filter glob, tạo / đổi tên / xoá / cut / copy / paste, mở trong Finder, binding datasource theo file. Panel kéo đổi độ rộng được (hiện đang cố định 240px).
- Quick open `Cmd+P`: bảng (mọi connection, fan-out stream), file, connection; fuzzy + `tableRank`; giữ lựa chọn theo id.
- Find in files `Cmd+Shift+F`: case / word / regex, file mask, Enter mở file và chọn đúng chỗ.

Nguồn: `files/SqlFilesPanel.vue`, `FileTreeNode.vue`, `stores/sqlFiles.ts`, `src/bun/sqlFiles.ts`, `quickopen/*`, `useQuickOpen.ts`, `useTableCatalog.ts`, `lib/fuzzy.ts`, `tableRank.ts`, `findinfiles/*`, `useFindInFiles.ts`, `src/bun/findInFiles.ts`, `scripts/fetch-rg.ts`.

What should know:
- Mọi đường dẫn đi qua `realpathInside` (chống thoát khỏi folder đã mở, symlink).
- `transferSqlEntries` trả kết quả từng entry.
- `rg 15.2.0` pin sha256 theo nền tảng trong `scripts/fetch-rg.ts`; trong app đặt vào resources, đọc bằng `mygo.App.Path(mygo.PathResources)`.
- `rg --json`; call mới kill call cũ; cap số dòng, cắt dòng dài; `splitMask` xử lý brace glob.

Gotchas:
- Dev (`mygo dev`) và bản build có đường dẫn resources khác nhau: kiểm cả hai.
- Đổi tên file đang mở: tab, bookmark (path), binding datasource phải đi theo.
- Quick open fan-out nhiều connection: huỷ lượt cũ khi gõ tiếp, bỏ kết quả về muộn.

### M6: Explain viewer, phím tắt, snippets, giao diện

Phạm vi:
- Explain: canvas (zoom/pan, nhiệt chi phí, node nóng nhất), operator tree, raw JSON; copy plan/tree.
- Settings phím tắt: ghi chord, tạm dừng dispatcher khi đang ghi, lưu store. Danh sách mặc định (`useShortcuts.ts`): `tab.close` ⌘W, `tab.closeOthers` ⌘⇧W, `tab.closeAll` ⌘⌥W, `tab.next/prev` ⌘⌥→/←, `tab.nextAlt/prevAlt` ⌃Tab/⌃⇧Tab, `quickOpen` ⌘P, `findInFiles` ⌘⇧F, `editor.formatSql` ⌥⇧F, `editor.explainPlan` ⌥⇧E, `result.sortAsc/Desc` ⌃⇧↑/↓, `result.rerun` ⌘R, `result.focusWhere` ⌘L.
- Snippets settings.
- Giao diện: UI font, grid font, UI scale (`c.SetZoom`); theme đã có.

Nguồn: `ExplainPlanViewer.vue`, `ExplainPlanDiagram.vue`, `ExplainPlanNodeTree.vue`, `lib/explainCanvas.ts`, `layout/KeyboardShortcutsSettings.vue`, `useShortcuts.ts`, `lib/shortcutDisplay.ts`, `editor/SnippetsSettings.vue`, `stores/snippets.ts`.

Gotchas:
- Canvas: `Painter.Transform` để pan/zoom, nhưng hit-test phải tự đổi toạ độ con trỏ qua nghịch đảo transform. Pinch trên Windows đến dưới dạng Ctrl+scroll.
- MySQL: UPDATE/DELETE với `FORMAT=JSON` chỉ ra `<not executable by iterator executor>`; muốn đủ phải fallback `EXPLAIN` dạng bảng như dbx (recall chưa làm). Parser giữ cả nhánh `query_plan` và `query_block` (server cũ).

### M7: Redis

Phạm vi: profile Redis (default db), key list (SCAN theo trang, kèm TYPE + TTL), xem/sửa string và hash (đổi tên field, only-if-absent), TTL, xoá key, new key, console (đọc tự do, ghi phải xác nhận).

Nguồn: `src/bun/redis/*` (`pool`, `ops`, `commandSafety`, `bytes`), `src/shared/redis.ts`, `components/redis/*`, `lib/redisFormat.ts`, tests `redisBytes`, `redisCommandSafety`, `redisFormat`, `redisOps`.

Gotchas:
- Một client cho mỗi logical database (recall).
- Key/value nhị phân: hiển thị UTF-8 nếu hợp lệ, không thì base64; key nhị phân vẫn thao tác được.
- `parseCommandLine` (quote) và allowlist lệnh đọc phải port kèm test.

### M8: Đóng gói

Phạm vi: `go tool mygo build`, icon (`resources/icon.png`), `rg` trong resources, thư viện sqlite native được CLI đóng vào app, cài vào `~/Applications`.

Gotchas:
- App đóng gói không bao giờ tải thư viện native lúc chạy: CLI phải đóng sẵn. Kiểm bằng cách chạy bản build trên máy, không qua `mygo dev`.
- Đổi identifier sau khi đã dùng thật = mất `PathUserData` cũ.
- Keychain: dùng `/usr/bin/security` như recall, không dùng Keychain API của mygo. Item tạo bằng API tin cdhash của bản build (cert local không có Team ID nên đổi sau mỗi build, macOS hỏi lại); item tạo bằng `security` tin chính `security`, không đổi. Item `seeker.*` tạo trước đó bằng API sẽ hỏi một lần: bấm Always Allow.

Xong khi: dùng Seeker thay recall hằng ngày.

---

## 6. Backlog sau parity (từ `../recall/TODO.md`, mục chưa tick)

- Phase 1: nút New connection khi sidebar trống, nút `+` ở sidebar, nút định vị tab hiện tại trong sidebar, menu edit connection.
- Phase 2: căn giữa nút new tab, lazy load nhóm trong table (FK/trigger chậm), sort qua context menu thay vì click tên cột.
- Phase 3: context ở file SQL, vùng run SQL, global search.
- Phase 5: reconnect trong context menu, nền modal tối hơn, viền phải cột cuối, icon theo loại DB (mysql/postgres).
- Phase 14: sum selection, Shift+click chọn vùng, auto width cột, format SQL, tab auto width.
- Phase 15: màu chữ theo kiểu dữ liệu, suggest field, nới thanh WHERE/ORDER BY, Cmd+L.
- Phase 16: pin table, generate INSERT ở context menu row number, generate DDL, cancel connect.
- Sau: xuống dòng trong cell, nút count theo kết quả query, copy raw plan/tree, sidebar index (tooltip điều kiện partial index, Copy DDL), khoảng trống dưới editor, giữ thứ tự cột khi reload, màn loading, cơ chế update (`plugins/updater`), Redis.
- Nhiều mục ở trên recall đã code xong nhưng chưa tick ("code xong, chờ test trong app"); khi port, coi code recall là chuẩn, không phải checkbox.

## 7. Ghi chú về recall

- `README.md`/`GUIDELINE.md` của recall ghi "không có test suite" nhưng `tests/` có 16 file.
- Mnemonic bookmark: `TODO.md` và `README.md` ghi khác nhau; code (`SqlEditor.vue` ~dòng 161–270) là chuẩn: gán `Ctrl+Shift+<ký tự>` hoặc `Ctrl+F11` rồi ký tự, nhảy `Cmd+<ký tự>`.
- Hai splitter của recall lệch nhau (xem M1, quyết định 15).

## User todo
- Số vào gutter mark cho book mark