package device

import (
	"fmt"
	"strings"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // postgres driver
	"github.com/pkg/errors"
)

var (
	fetchDevicesDEP = `INSERT INTO devices (
	serial_number, 
	model, 
	description, 
	color, 
	asset_tag,
	dep_profile_status,
	dep_profile_uuid,
	dep_profile_assign_time,
	dep_profile_push_time,
	dep_profile_assigned_date,
	dep_profile_assigned_by,
	dep_device
	) 
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	ON CONFLICT (serial_number)
	DO UPDATE SET
	model = $2,
	description = $3,
	color = $4,
	asset_tag = $5,
	dep_profile_status = $6,
	dep_profile_uuid = $7,
	dep_profile_assign_time = $8,
	dep_profile_push_time = $9,
	dep_profile_assigned_date = $10,
	dep_profile_assigned_by = $11,
	dep_device = $12
	RETURNING device_uuid;`

	authenticateMDM = `INSERT INTO devices (
	udid, 
	apple_mdm_topic,
	os_version,
	build_version,
	product_name,
	serial_number,
	imei,
	meid
	)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8) 
    ON CONFLICT (serial_number)
    DO UPDATE SET 
	udid=$1,
	apple_mdm_topic=$2,
    os_version=$3,
    build_version=$4,
    product_name=$5,
    serial_number=$6,
    imei=$7,
    meid=$8
	RETURNING device_uuid;`

	selectDevicesStmt = `SELECT device_uuid, 
	udid,
	serial_number, 
	dep_profile_status,
	model,
	workflow_uuid
	FROM devices`
)

// Datastore manages devices in a database
type Datastore interface {
	New(src string, d *Device) (string, error)
	GetDeviceByUDID(udid string, fields ...string) (*Device, error)
	Devices(params ...interface{}) ([]Device, error)
}

// UUID is a filter that can be added as a parameter to narrow down the list of returned results
type UUID struct {
	UUID string
}

func (p UUID) where() string {
	return fmt.Sprintf("device_uuid = '%s'", p.UUID)
}

type pgStore struct {
	*sqlx.DB
}

func (store pgStore) GetDeviceByUDID(udid string, fields ...string) (*Device, error) {
	var device Device
	s := strings.Join(fields, ", ")
	query := `SELECT ` + s + ` FROM devices WHERE udid=$1 LIMIT 1`
	return &device, sqlx.Get(store, &device, query, udid)
}

func (store pgStore) New(src string, d *Device) (string, error) {
	switch src {
	case "fetch":
		err := store.QueryRow(
			fetchDevicesDEP,
			d.SerialNumber,
			d.Model,
			d.Description,
			d.Color,
			d.AssetTag,
			d.DEPProfileStatus,
			d.DEPProfileUUID,
			d.DEPProfileAssignTime,
			d.DEPProfilePushTime,
			d.DEPProfileAssignedDate,
			d.DEPProfileAssignedBy,
			true,
		).Scan(&d.UUID)
		if err != nil {
			return "", err
		}
		return d.UUID, nil
	case "authenticate":
		err := store.QueryRow(
			authenticateMDM,
			d.UDID,
			d.MDMTopic,
			d.OSVersion,
			d.BuildVersion,
			d.ProductName,
			d.SerialNumber,
			d.IMEI,
			d.MEID,
		).Scan(&d.UUID)
		if err != nil {
			return "", err
		}
		return d.UUID, nil
	default:
		return "", fmt.Errorf("datastore command not supported %q", src)
	}
}

func (store pgStore) Devices(params ...interface{}) ([]Device, error) {
	stmt := selectDevicesStmt
	stmt = addWhereFilters(stmt, params...)
	var devices []Device
	err := store.Select(&devices, stmt)
	if err != nil {
		return nil, errors.Wrap(err, "pgStore Devices")
	}
	return devices, nil
}

// whereer is for building args passed into a method which finds resources
type whereer interface {
	where() string
}

// add WHERE clause from params
func addWhereFilters(stmt string, params ...interface{}) string {
	var where []string
	for _, param := range params {
		if f, ok := param.(whereer); ok {
			where = append(where, f.where())
		}
	}

	if len(where) != 0 {
		whereFilter := strings.Join(where, ",")
		stmt = fmt.Sprintf("%s WHERE %s", stmt, whereFilter)
	}
	return stmt
}

//NewDB creates a Datastore
func NewDB(driver, conn string, logger kitlog.Logger) (Datastore, error) {
	switch driver {
	case "postgres":
		db, err := sqlx.Open(driver, conn)
		if err != nil {
			return nil, errors.Wrap(err, "device datastore")
		}
		var dbError error
		maxAttempts := 20
		for attempts := 1; attempts <= maxAttempts; attempts++ {
			dbError = db.Ping()
			if dbError == nil {
				break
			}
			logger.Log("msg", fmt.Sprintf("could not connect to postgres: %v", dbError))
			time.Sleep(time.Duration(attempts) * time.Second)
		}
		if dbError != nil {
			return nil, errors.Wrap(dbError, "device datastore")
		}
		migrate(db)
		return pgStore{DB: db}, nil
	default:
		return nil, errors.New("unknown driver")
	}
}

func migrate(db *sqlx.DB) {
	schema := `
	CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
	CREATE TABLE IF NOT EXISTS devices (
	  device_uuid uuid PRIMARY KEY 
	            DEFAULT uuid_generate_v4(), 
	  udid text NOT NULL DEFAULT '',
	  serial_number text,
	  os_version text,
	  model text,
	  color text,
	  asset_tag text,
	  dep_profile_status text,
	  dep_profile_uuid text,
	  dep_profile_assign_time date,
	  dep_profile_push_time date,
	  dep_profile_assigned_date date,
	  dep_profile_assigned_by text,
	  description text,
	  build_version text,
	  product_name text,
	  imei text,
	  meid text,
	  apple_mdm_token text,
	  apple_mdm_topic text,
	  apple_push_magic text,
	  mdm_enrolled boolean,
	  workflow_uuid text NOT NULL DEFAULT '',
	  dep_device boolean,
	  awaiting_configuration boolean
	  );
	  CREATE UNIQUE INDEX IF NOT EXISTS serial_idx ON devices (serial_number);`
	db.MustExec(schema)
}
