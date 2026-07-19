package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func aliases(pool *pgxpool.Pool, args []string) {
	if len(args) < 2 {
		fatal(fmt.Errorf("usage: aliases <talkgroups|radios> <export|import>"))
	}
	kind, action := args[0], args[1]
	if kind != "talkgroups" && kind != "radios" {
		fatal(fmt.Errorf("unknown alias kind"))
	}
	table, idcol := "talkgroup_aliases", "talkgroup_id"
	if kind == "radios" {
		table, idcol = "radio_aliases", "radio_id"
	}
	switch action {
	case "export":
		w := csv.NewWriter(os.Stdout)
		defer w.Flush()
		_ = w.Write([]string{"system_id", idcol, "alias", "description", "category", "enabled", "source"})
		rows, err := pool.Query(context.Background(), fmt.Sprintf(`SELECT system_id,%s,coalesce(alias,''),coalesce(description,''),coalesce(category,''),enabled,source FROM %s ORDER BY system_id,%s`, idcol, table, idcol))
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c, d, e, f, g string
			if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g); err != nil {
				fatal(err)
			}
			_ = w.Write([]string{a, b, c, d, e, f, g})
		}
	case "import":
		fs := flag.NewFlagSet("aliases import", flag.ExitOnError)
		file := fs.String("file", "", "UTF-8 CSV file")
		dry := fs.Bool("dry-run", false, "validate only")
		overwrite := fs.Bool("overwrite-manual", false, "allow overwrite manual aliases")
		_ = fs.Parse(args[2:])
		if *file == "" {
			fatal(fmt.Errorf("--file is required"))
		}
		f, err := os.Open(*file)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r := csv.NewReader(f)
		_, err = r.Read()
		if err != nil {
			fatal(err)
		}
		line := 1
		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			line++
			if err != nil || len(row) != 7 || row[0] == "" || row[1] == "" {
				fatal(fmt.Errorf("invalid CSV row %d", line))
			}
			if *dry {
				continue
			}
			query := fmt.Sprintf(`INSERT INTO %s(system_id,%s,alias,description,category,enabled,source) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(system_id,%s) DO UPDATE SET alias=EXCLUDED.alias,description=EXCLUDED.description,category=EXCLUDED.category,enabled=EXCLUDED.enabled,source=EXCLUDED.source,updated_at=now() WHERE %s.source<>'manual' OR $8`, table, idcol, idcol, table)
			_, err = pool.Exec(context.Background(), query, row[0], row[1], row[2], row[3], row[4], row[5], row[6], *overwrite)
			if err != nil {
				fatal(err)
			}
		}
		fmt.Println("alias import complete")
	default:
		fatal(fmt.Errorf("unknown alias action"))
	}
}
