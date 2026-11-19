- Tham khảo ../recall khi có những thứ cần đưa ra nghiệp vụ hoặc kỹ thuật 
- - `ui.TextInput` có padding dọc mặc định: ép `.Height(n)` nhỏ hơn mặc định mà không đặt `.Padding(0, x)` thì chữ bị đẩy lệch xuống dưới. Input thấp (vd ô số trang ở status bar) luôn kèm `.Padding(0, x)`, như `clauseField` (`Height(26).Padding(0, 10)`).

## Test với database thật
`go test ./...` tự `t.Skip` các test live (Postgres, MySQL, Redis) khi thiếu biến môi trường, và skip vẫn tính là pass. "Xanh" không có các biến dưới đây thì chưa gồm test live; báo kết quả phải nói rõ.

Đụng tới driver, pool, grid, table tab, run/explain hoặc redis thì chạy bản đầy đủ trước khi báo xanh hay commit:

```sh
(cd ../recall && docker compose up -d --wait postgres mysql redis)
SEEKER_TEST_PG_URL=postgres://postgres:postgres@127.0.0.1:55432/recall_test \
SEEKER_TEST_MYSQL_URL=mysql://root:mysql@127.0.0.1:53306/recall_test \
SEEKER_TEST_REDIS=127.0.0.1:56379 \
CGO_ENABLED=0 go test -count=1 ./...
(cd ../recall && docker compose stop)
```
