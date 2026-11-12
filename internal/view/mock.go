package view

import (
	"fmt"
	"strings"
)

// Mock data for reviewing the layout; replaced by the stores once the
// backend lands (M1).

const mockSQL = `-- Revenue by country, last 30 days
select u.country,
       count(distinct o.id) as orders,
       sum(o.total)         as revenue
from orders o
join users u on u.id = o.user_id
where o.created_at >= now() - interval '30 days'
  and o.status = 'paid'
group by u.country
order by revenue desc
limit 50;

select * from users where email ilike '%@example.com';
`

type connInfo struct {
	name, dialect string
	connected     bool
}

var mockConns = []connInfo{
	{"local-pg", "PostgreSQL 17", true},
	{"staging-mysql", "MySQL 8.4", false},
	{"cache-redis", "Redis 7", false},
}

// mockTree maps a tree path ("conn/db/schema/table/group") to its children.
var mockTree = func() map[string][]string {
	t := map[string][]string{
		"local-pg":                             {"app_db", "analytics"},
		"local-pg/app_db":                      {"public", "audit"},
		"local-pg/app_db/public":               {"orders", "order_items", "payments", "products", "sessions", "users"},
		"local-pg/app_db/audit":                {"events"},
		"local-pg/analytics":                   {"public"},
		"local-pg/analytics/public":            {"daily_revenue"},
		"staging-mysql":                        {"shop"},
		"staging-mysql/shop":                   {"customers", "invoices"},
		"local-pg/app_db/public/users":         {"Columns", "Indexes", "Foreign keys", "Triggers"},
		"local-pg/app_db/public/users/Columns": {"id", "email", "name", "status", "country", "plan", "created_at"},
		"local-pg/app_db/public/users/Indexes": {"users_pkey", "users_email_key"},
	}
	out := map[string][]string{}
	for parent, kids := range t {
		for _, k := range kids {
			out[parent] = append(out[parent], parent+"/"+k)
		}
	}
	return out
}()

func treeChildren(path string) []string {
	if kids, ok := mockTree[path]; ok {
		return kids
	}
	// Every table opens onto its groups, even those the mock has no rows for.
	if strings.Count(path, "/") == 3 {
		return []string{path + "/Columns", path + "/Indexes"}
	}
	return nil
}

var mockFiles = map[string][]string{
	"~/sql":            {"~/sql/reports", "~/sql/migrations", "~/sql/scratch.sql", "~/sql/revenue.sql"},
	"~/sql/reports":    {"~/sql/reports/churn.sql", "~/sql/reports/cohorts.sql"},
	"~/sql/migrations": {"~/sql/migrations/001_init.sql", "~/sql/migrations/002_orders.sql"},
}

var mockColumns = []string{"id", "email", "name", "status", "country", "plan", "total", "created_at"}

func mockRows(n int) [][]string {
	statuses := []string{"active", "trial", "churned", "active"}
	countries := []string{"VN", "SG", "JP", "US", "DE"}
	plans := []string{"free", "pro", "team"}
	rows := make([][]string, n)
	for i := range rows {
		name := fmt.Sprintf("User %d", i+1)
		if i%7 == 3 {
			name = "" // shows as NULL
		}
		rows[i] = []string{
			fmt.Sprint(1000 + i),
			fmt.Sprintf("user%d@example.com", i+1),
			name,
			statuses[i%len(statuses)],
			countries[i%len(countries)],
			plans[i%len(plans)],
			fmt.Sprintf("%.2f", float64((i*7919)%100000)/100),
			fmt.Sprintf("2026-%02d-%02d 09:%02d:%02d.%06d+07", i%12+1, i%28+1, i%60, (i*13)%60, (i*104729)%1000000),
		}
	}
	return rows
}
