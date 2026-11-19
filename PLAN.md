# Seeker: port recall sang mygo native UI

Nguồn: `../recall` (Electrobun + Bun + Vue). Đích: Go + fork `../mygo` (package `ui` vẽ native, không webview). Port xong M0–M8 (2026-11-19), Seeker đã thay recall dùng hằng ngày. File này giữ quyết định, kiến thức và chỗ Seeker cố ý khác recall; lịch sử từng phase xem git log.

---

## 1. Quyết định đã chốt

1. App DB dùng `plugins/sqlite` của mygo, bọc sau `internal/store` (`modernc.org/sqlite` là phương án dự phòng).
2. Format SQL: goja + bundle `sql-formatter` 15.9.0 (giữ y hệt recall: chỉ set dialect, giữ case, indent 2). GoSQLX đã thử và loại: viết lại SQL (`::` → `CAST`, `JOIN` → `INNER JOIN`), tự viết hoa keyword, không thụt lề subquery/CTE.
3. Find in files: bundle `rg` như recall.
4. Dữ liệu recall: import connections, bookmarks, pinned tables từ `recall.db` một lần; password keychain move từ `recall.connection.<id>` sang `seeker.connection.<id>`.
5. Tên hiển thị `Seeker`, module Go `seeker`.
6. Layout island: bo 8px, khe 6px, title bar trong suốt trên nền canvas.
7. Theme kiểu Zed: Appearance System/Light/Dark + theme sáng + theme tối + accent. Default Light/Dark + 4 flavor Catppuccin (palette v1.8.0), 14 accent. Mỗi kiểu dữ liệu trong grid một màu lấy từ theme, chuỗi giữ màu chữ.
8. mygo dùng fork `../mygo` qua `replace`. Thay đổi trong fork (ảo hoá cột, cột ghim của `ui.Table`, `editor.State.Rect`) là option opt-in.
9. Identifier `io.starchaser.seeker`: keychain và quyền macOS bám theo nó, không đổi nữa. `PathUserData` theo **tên app** (`~/Library/Application Support/<App.Name()>`, mygo `paths.go:90`): bản build là `Seeker`, `mygo dev` là `Seeker Dev` (hai DB riêng). Đổi `name` trong `mygo.json` = mất dữ liệu cũ.
10. Git: remote `origin` = `git@github.com:prometheusalpha/seeker.git`.
11. Script chỉ có comment: `sqltext.Split` bỏ đoạn không có code.
12. Session chạy SQL: **mỗi tab một session** (key `connection, database, tab`): giữ `SET`/temp table/`BEGIN` của tab, hai tab cùng database chạy song song. Session catalog (sidebar): tối đa 3 mỗi (connection, database); MySQL dùng session của profile cho mọi database.
13. Xoá file (hoặc folder chứa nó) thì đóng tab luôn, kể cả tab đang sửa dở, không hỏi, không vào stack ⌘⇧T (2026-11-19).
14. Icon engine: devicon 2.16.0 (solid, tô theo theme); icon khác: lucide 0.470.0.
15. Bỏ: Snippets settings, đổi phím `⌘↵` và bookmark, `schema.table` trong Quick open.

## 2. Kiến trúc

```
main.go       cửa sổ 1280×820, TitleBarHidden, StateKey "main"
internal/
  app/        state của app (tab, editor, grid, table tab, files, quick open, shortcuts…); view chỉ đọc và gọi
  view/       UI mygo
  model/      Profile, Bookmark, PinnedTable
  store/      app SQLite (plugin), import recall
  secret/     keychain (macOS qua /usr/bin/security, nơi khác mygo.Secrets)
  driver/     Conn = một session; postgres (pgx v5), mysql (go-sql-driver)
  pool/       session catalog + session theo tab, idle probe, thay session chết
  redis/      go-redis v9, commandSafety, format
  grid/       formatCell, kind, width, columnWindow/Order, sum, export, sqlInsert, rowUpdate
  sqltext/    Split/At, scope (completion), clause (WHERE/ORDER BY)
  explain/    parse plan PG/MySQL + layout canvas
  sqlfmt/     goja + sql-formatter (bundle bằng gen.sh)
  files/      scan, ops (create/rename/transfer/trash), find (rg)
  rank/       tableRank + fuzzy
  theme/      Settings → ui.Theme + Islands + editor.Theme + Types
  icons/      svg go:embed
  testenv/    helper test (sqlite native lib)
```

Mỗi file < 1000 dòng. Không giữ `ui.Context` hay `ui.Element` trong state.

## 3. Kiến thức chung

### mygo native UI
- View chạy lại nhiều lần mỗi event. Không side effect lúc build; I/O chạy trong goroutine, đẩy kết quả về bằng `Window.Update`. Kết quả cũ phải bị bỏ (`gen`) khi nguồn đã đổi.
- `ui.Element` hết hạn sau mỗi lần build. Dùng `c.Services()` cho callback lâu dài, `ui.Handle` + `.Bind` cho identity.
- Trong vòng lặp phải `c.Key(...)`. Trùng key: Tester panic, app chỉ log.
- Giá trị bind cập nhật sau khi build; `Changed()`/`Submitted` báo ở lần build sau.
- `c.Theme()` **trước** `c.SetTheme` là theme hệ thống của frame đó. Đọc `Dark` hệ thống ở đầu `View`.
- Hàng được chọn tô nền accent, chữ `AccentText`. Mọi màu tự gán phải đổi theo, dùng `ink()`.
- `Split` vẽ divider 1px `t.Border`; `gapSplit` bọc theme để divider trùng canvas. `ui.Split` chỉ cho độ rộng pane **đầu**: panel bên phải chạy split `RightToLeft`, `Direction` đặt trên `ui.Box` bọc ngoài **trước khi dựng Split**, mỗi pane bọc lại `LeftToRight`; `gapSplit` có `rtl` để đổi phía khoảng cách.
- `ui.Table` cao dòng `Space(8)`; grid dày hơn bằng `ui.Themed` với `Spacing = 3`.
- `Outline` gọi `children` cho mọi row đang hiện để biết row có mũi tên: row đóng phải trả slice rỗng (`treeChildren`), không thì mở app là dial hết server. `Tree` build hết, không dùng cho sidebar.
- Bắt `row.Clicked()` ở từng dòng thì list không tự chọn dòng: phải gán row chọn bằng tay.
- `ui.TextInput` ép `.Height(n)` nhỏ thì phải kèm `.Padding(0, x)` (xem AGENTS.md).
- Redraw theo thời gian (đồng hồ chạy): `c.After(d)`.
- Tester: `Scroll` dương là phải/xuống; animation phải `tt.Advance(...)`; vẽ bằng CPU, số đo chỉ so tương đối. Test cửa sổ không cần DB: `filesWindow`, hoặc `editorWindow` với profile giả `127.0.0.1:1`.
- Picker native (`pickSave`, `pickExport`, `pickFolder`) đọc ở goroutine gọi, trước `go func()`, để test đổi được mà không race.

### Driver và session
- **pgx:** SQL của user chạy `QueryExecModeSimpleProtocol` (text, giữ microsecond, không lỗi `cached plan must not change result type` sau DDL); introspection dùng extended protocol. Cancel bằng `CancelRequestContextWatcherHandler` (giữ session, cắt socket sau 5s nếu server im).
- **go-sql-driver:** huỷ context chỉ đóng socket, query vẫn chạy trên server → cancel bằng `KILL QUERY <thread_id>` từ connection thứ hai. Session-state chỉ an toàn trên `*sql.Conn` giữ riêng. DSN `parseTime=false`, `multiStatements=false`. `allowPublicKeyRetrieval=false` nghĩa là bắt buộc TLS verify-full (driver luôn tự lấy RSA key). `SELECT SLEEP()` bị kill trả 1, không lỗi.
- Idle probe: session idle ≥ 5s thì ping trước (cap 5s), lỗi thì thay session. Lý do: NAT cắt socket im lặng, request sau treo quá 300s.
- Thay session chết: so con trỏ, không so key (recall dính toast "Connection lost" giả).
- Không replay statement khi mở lại session (`WITH x AS (INSERT …) SELECT` trông như câu đọc).
- Vị trí lỗi PG là offset **ký tự** 1-based, editor dùng byte: đổi rune → byte rồi cộng `Statement.Start`. MySQL không trả vị trí.
- Cap 20000 dòng mỗi statement. Explain không bao giờ `ANALYZE`. MySQL 8.4 `explain_json_format_version=2` để cây ở gốc document: parser nhận cả ba dạng.
- Thời gian: `timestamptz` và `TIMESTAMP` của MySQL là instant, hiển thị `FixedZone(+07)`, giữ microsecond; `timestamp` PG là wall clock. Binary: `sqlInsert` ghi hex (`X'..'`, `'\x..'`).
- **sqlite plugin:** không `BEGIN/COMMIT` tay, chỉ `Transaction(ctx, []Statement)`; mỗi `Execute` một câu; chặn `ATTACH`.
- sqlfmt chạy ngoài UI thread; lỗi parse cắt dòng đầu, ≤ 200 ký tự.

### Fork mygo
- Sửa fork thì test trong fork theo style repo đó, chỉ commit khi user bảo; push user tự làm.
- Plugin sqlite với fork local: test dùng `testenv.SQLite(t)` (tải bản publish, kiểm sha256, set `MYGO_NATIVE_SQLITECORE`; lần đầu ~30s). Máy không có Zig, đừng chạy `mygo native build`.

### Đóng gói
- `install.sh`: build `--skip-dmg`, `codesign --verify --deep --strict`, cài vào `~/Applications/Seeker.app`. `dev.sh`: `mygo dev -sign` cùng cert.
- Ký bằng cert local "Telepath Local Signing" (không Team ID), hardened runtime; `entitlements.plist` tắt library validation để dlopen dylib sqlite.
- Bundle có `AppIcon.icns`, `rg` 15.2.0 (`scripts/fetch-rg.sh`, kiểm sha256), `libSqlitecore.dylib`. App đóng gói không tải thư viện native lúc chạy; kiểm bằng `lsof` trên bản build. Chỉ build darwin-arm64.
- Keychain qua `/usr/bin/security`: item tạo bằng Keychain API tin cdhash của bản build, đổi sau mỗi build nên macOS hỏi lại.

### Quy trình
- Lệnh kiểm: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go tool mygo vet . && CGO_ENABLED=0 go test ./...`. Test live: xem AGENTS.md.
- Khẳng định nguyên nhân bug phải kèm dòng code hoặc số liệu chạy thật. Đừng tin báo cáo "xong" của subagent, tự chạy lại.
- Sửa xong báo user test trong app rồi mới coi là xong.
- Comment ghi quyết định, không TLDR; không hard-wrap markdown.
- Commit: không Co-Author; chỉ commit khi user bảo.
- Ưu tiên lưu SQLite (`store.SetSetting`) hơn file rời.
- Setup để test (file tạm, ảnh chụp) làm xong phải xoá. Kiểm layout: test tạm render `tt.Image()` ra PNG rồi xem, xong xoá.

## 4. Chỗ Seeker cố ý khác recall

- Sidebar: lọc server-side, debounce 250ms; pin sort lên đầu anh em, không section riêng. Save connection đóng dialog ngay, lỗi hiện ở tooltip của row.
- Tab: stack mở lại tab đã đóng (⌘⇧T, 20 tab). Tab nháp ⌘S mở hộp Save native.
- Editor: completion không chặn phím (cache nạp nền, 5 phút / lỗi 10 giây). Bookmark gutter kiểu DataGrip (thẻ vuông, ký tự hoa sau số dòng).
- Grid: không sort khi bấm tên cột (sort ghi vào ô ORDER BY); hộp giá trị chỉ hiện khi ô bị cắt hoặc có xuống dòng; export qua hộp Save native; count của table tab ở status bar (`đã-tải/tổng`, `N+` bấm để đếm).
- Shortcuts: một chord chỉ thuộc một lệnh, lệnh đang giữ bị unbind kèm toast.
- Quick open: chỉ connection đang kết nối, không tự kết nối; PG đọc mọi database của server; cache 5 phút (30 giây khi đọc thiếu); query trống liệt kê hết.
- Loading: table tab, query và Explain đang chạy hiện đồng hồ giây và nút Cancel; cancel giữ session.

## 5. Còn thiếu (biết, chưa làm)

- Grid: gợi ý WHERE/ORDER BY chỉ cho từ cuối ô, không fuzzy; layout cột/lọc chỉ sống trong phiên; `Copy as INSERT` theo thứ tự result.
- Find in files: chỉ tô hit đầu của dòng; không điền sẵn query từ vùng chọn.
- Explain: zoom chỉ bằng nút; MySQL UPDATE/DELETE chưa fallback `EXPLAIN` dạng bảng.
- Redis: không xoá nhiều key, không phát hiện mất kết nối, không có trong Quick open, TLS không có tuỳ chọn CA/verify, zset/stream chưa hỗ trợ (console vẫn dùng được).
- Đóng cửa sổ không chặn tab bẩn; không xoá được connection.

## 6. Ghi chú về recall

- Mnemonic bookmark: code (`SqlEditor.vue` ~dòng 161–270) là chuẩn, không theo `TODO.md`/`README.md`.
- `README.md`/`GUIDELINE.md` ghi "không có test suite" nhưng `tests/` có 16 file.
