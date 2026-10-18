-- +goose Up
CREATE TABLE server_disk_mount_metrics (
 at_unix INTEGER NOT NULL REFERENCES server_metrics(at_unix) ON DELETE CASCADE,
 mount TEXT NOT NULL CHECK (mount IN ('config','media')),
 read_mbps REAL,
 write_mbps REAL,
 read_iops REAL,
 write_iops REAL,
 average_latency_ms REAL,
 PRIMARY KEY (at_unix, mount)
);
-- +goose Down
DROP TABLE server_disk_mount_metrics;