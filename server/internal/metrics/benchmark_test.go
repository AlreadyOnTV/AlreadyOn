package metrics

import (
	"context"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"testing"
	"time"
)

func BenchmarkSampler(b *testing.B) {
	s := NewSampler(b.TempDir())
	defer s.Close()
	for b.Loop() {
		s.Read(time.Now())
	}
}

// Full retained history, isolated from any user's database.
func BenchmarkHistory(b *testing.B) {
	db, err := storage.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	now := time.Unix(1800000000, 0)
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 43200; i++ {
		at := now.Unix() - int64(i)
		if _, err = tx.Exec(`INSERT INTO server_metrics(at_unix,cpu,ram,ingress_mbps,egress_mbps,network_interface,network_scope,disk_read_mbps,disk_write_mbps,io_pressure) VALUES(?,25,40,2,3,'eth0','host',4,5,1)`, at); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 16; j++ {
			if _, err = tx.Exec(`INSERT INTO server_cpu_core_metrics(at_unix,core,utilization) VALUES(?,?,50)`, at, j); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, minutes := range []int{15, 720} {
		b.Run(fmt.Sprintf("%dmin", minutes), func(b *testing.B) {
			for b.Loop() {
				if _, err := ReadHistory(context.Background(), db.DB, minutes, now); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("%dmin-cached", minutes), func(b *testing.B) {
			var reader HistoryReader
			if _, err := reader.Read(context.Background(), db.DB, minutes, now); err != nil {
				b.Fatal(err)
			}
			for b.Loop() {
				if _, err := reader.Read(context.Background(), db.DB, minutes, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkStoreSample(b *testing.B) {
	db, err := storage.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	s := Sample{At: 1800000000, CPU: percent(25), RAM: percent(40), DiskScope: "server"}
	for j := 0; j < 16; j++ {
		s.CPUCores = append(s.CPUCores, CoreSample{j, percent(50)})
	}
	for b.Loop() {
		s.At++
		if err := storeSample(context.Background(), db.DB, s); err != nil {
			b.Fatal(err)
		}
	}
}
