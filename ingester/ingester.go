//stateful function that reads messages from Kafka topic and writes to data lake
//event messages are put on to Kafka by the ingest REST endpoint

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/storage"
	"github.com/apache/flink-statefun/statefun-sdk-go/v3/pkg/statefun"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/source"
	"github.com/xitongsys/parquet-go/writer"
	"github.com/xitongsys/parquet-go/parquet"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	
)

// default database connection settings
const (
	db_host_def     = "rtdl-db"
	db_port_def     = 5433
	db_user_def     = "rtdl"
	db_password_def = "rtdl"
	db_dbname_def   = "rtdl_db"
)


// create the `psqlCon` string used to connect to the database
var psqlCon string

// Dremio host and port
var dremioHost string
var dremioPort string

// header for Dremio communication
var dremioToken string


//Incoming message would have
// - a source key to identify the stream
// - a message type that can be used to indicate the message purpose
// - generic payload

type IncomingMessage struct {
	StreamId   string                  `json:"stream_id,omitempty"`
	StreamAltId string				   `json:"stream_alt_id,omitempty"`
	MessageType string                 `json:"message_type,omitempty"`
	Payload     map[string]interface{} `json:"payload"`
}

//struct representation of stream configuration
// StreamAltId is applicable where the stream is being fed from an external system and the alternate id
// represents the unique identifier for that system
type Config struct {
	StreamId           sql.NullString `db:"stream_id" default:""`
	StreamAltId        sql.NullString `db:"stream_alt_id" default:""`
	Active             sql.NullBool   `db:"active"`
	MessageType        sql.NullString `db:"message_type" default:""`
	FileStoreTypeId    sql.NullInt64  `db:"file_store_type_id"`
	Region             sql.NullString `db:"region" default:""`
	BucketName         sql.NullString `db:"bucket_name" default:""`
	FolderName         sql.NullString `db:"folder_name" default:""`
	PartitionTimeId    sql.NullInt64  `db:"partition_time_id"`
	CompressionTypeId  sql.NullInt64  `db:"compression_type_id"`
	AWSAcessKeyID      sql.NullString `db:"aws_access_key_id" default:""`
	AWSSecretAcessKey  sql.NullString `db:"aws_secret_access_key" default:""`
	GCPJsonCredentials sql.NullString `db:"gcp_json_credentials" default:""`
	CreatedAt          time.Time      `db:"created_at"`
	UpdatedAt          time.Time      `db:"updated_at"`
}

var configs []Config

//struct represenation of file store types
type FileStoreType struct {
	FileStoreTypeId   int64  `db:"file_store_type_id"`
	FileStoreTypeName string `db:"file_store_type_name"`
}

var fileStoreTypes []FileStoreType

//struct representation of partition times
type PartitionTime struct {
	PartitionTimeId   int64  `db:"partition_time_id"`
	PartitionTimeName string `db:"partition_time_name"`
}

var partitionTimes []PartitionTime

type CompressionType struct {
	CompressionTypeId   int64  `db:"compression_type_id"`
	CompressionTypeName string `db:"compression_type_name"`
}

var compressionTypes []CompressionType

//name variables for stateful function
var (
	IngestTypeName      = statefun.TypeNameFrom("com.rtdl.sf/ingest")
	KafkaEgressTypeName = statefun.TypeNameFrom("com.rtdl.sf/egress")
	IncomingMessageType = statefun.MakeJsonType(statefun.TypeNameFrom("com.rtdl.sf/IncomingMessage"))
)

//GCP config structure
type GCPCredentials struct {

	accountType	string	`json:"type"`
	projectId	string	`json:"project_id"`
	privateKeyId	string	`json:"private_key_id"`	
	privateKey	string	`json:"private_key"`
	clientEmail	string	`json:"client_email"`
	clientId	string	`json:"client_id"`
	authUri		string	`json:"auth_uri"`
	tokenUri	string	`json:"token_uri"`
	authProviderX509CertUrl	string `json:"auth_provider_x509_cert_url"`
	clientX509CertUrl	string	`json:"client_x509_cert_url"`

}


// GetEnv get key environment variable if exist otherwise return defalutValue
func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}
	return value
}

//loads all stream configurations
func LoadConfig() error {

	//temp variables - to be assigned to parent level variables on successful load
	var tempConfigs []Config
	var tempFileStoreTypes []FileStoreType
	var tempPartitionTimes []PartitionTime
	var tempCompressionTypes []CompressionType
	
	//directly load data from PostgreSQL
	
	

	// open database
	db, err := sqlx.Open("postgres", psqlCon)
	if err != nil {
		log.Println("Failed to open a DB connection: ", err)
		return err
	}

	configSql := "SELECT * FROM streams"

	err = db.Select(&tempConfigs, configSql) //populate stream configurations into array of stream config structs
	if err != nil {
		log.Println("Failed to execute query: ", err)
		return err
	}
	
	configs = tempConfigs

	fileStoreTypeSql := "SELECT * FROM file_store_types"
	err = db.Select(&tempFileStoreTypes, fileStoreTypeSql) //populate supported file store types
	if err != nil {
		log.Println("Failed to execute query: ", err)
		return err
	}
	
	fileStoreTypes = tempFileStoreTypes

	partitionTimesSql := "SELECT * FROM partition_times"
	err = db.Select(&tempPartitionTimes, partitionTimesSql) //populate supported file store types
	if err != nil {
		log.Println("Failed to execute query: ", err)
		return err
	}
	
	partitionTimes = tempPartitionTimes

	compressionTypesSql := "SELECT * from compression_types"
	err = db.Select(&tempCompressionTypes, compressionTypesSql)
	if err != nil {
		log.Println("Failed to execute query: ", err)
		return err
	}
	
	compressionTypes = tempCompressionTypes

	defer db.Close()
	log.Println("No. of config records retrieved : " + strconv.Itoa(len(configs)))
	return nil
}

//generic function for Dremio request response
func DremioReqRes (endPoint string, data []byte) (map[string] interface {}, error) {

	var version string
	var method string
	var request *http.Request
	var err error

	if endPoint == "login" { //end point v2
	
		version = "apiv2"
	
	} else {
	
		version = "api/v3"
	
	}

	url := "http://" + dremioHost + ":" + dremioPort + "/" + version + "/" + endPoint

	if data == nil { //Get request
		method = "GET"
	} else {
		method = "POST"
	}
	
	if data == nil {
	
		request, err = http.NewRequest(method, url, nil)
	
	} else {
	
		request, err = http.NewRequest(method, url, bytes.NewBuffer(data))
	
	}
	
	if err != nil {
		log.Println("Error communicating with Dremio server ", err)
		return nil, err
		
	}
	
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	
	if endPoint != "login" { //need to set auth header for non-login calls
	
		request.Header.Set("authorization", dremioToken)
	}
	
	client := &http.Client{}
	response, error := client.Do(request)
	if error != nil {
		return nil, error
	}
	defer response.Body.Close()

	body, _ := ioutil.ReadAll(response.Body)
	
	var dremioResponse map[string] interface {}
	
	err = json.Unmarshal(body, &dremioResponse)
	
	if err != nil {
		return nil, err
	} 

	return dremioResponse, nil
	
	
}

//connect to Dremio server and retrieve token for subsequent calls
func SetDremioToken() error {

	username := GetEnv("DREMIO_USERNAME", "rtdl")
	password := GetEnv("DREMIO_PASSWORD", "rtdl1234")
	
	
	loginData := []byte(`{"userName":"` + username + `", "password":"` + password + `"}`)
	
	dremioResponse, err := DremioReqRes("login", loginData)
	
	if err != nil {
	
		log.Println("Error retrieving Dremio token ", err)
		return err
	}
	
	dremioToken = fmt.Sprint(dremioResponse["token"])
	
	return nil

}

//initialize Dremio connection
func SetDremioConnection() error {

	// set Dremio host and port for use in other calls	
	dremioHost = GetEnv("DREMIO_HOST","host.docker.internal")
	dremioPort = GetEnv("DREMIO_PORT", "9047")
	
	err := SetDremioToken()
	
	if err != nil {
		return err
	}
	
	
	return nil

}


//	FUNCTION
// 	SetDBConnectionString
//	created by Gavin
//	on 20220109
//	last updated 20220111
//	by Gavin
//	Description:	(Copied from config-service.go)
//					Sets the `psqlCon` global variable. Looks up environment variables
//					and defaults if none are present.
func SetDBConnectionString() {
	var db_host, db_port, db_user, db_password, db_dbname = db_host_def, db_port_def, db_user_def, db_password_def, db_dbname_def
	var db_host_env, db_user_env, db_password_env, db_dbname_env = os.Getenv("RTDL_DB_HOST"), os.Getenv("RTDL_DB_USER"), os.Getenv("RTDL_DB_PASSWORD"), os.Getenv("RTDL_DB_DBNAME")
	db_port_env, err := strconv.Atoi(os.Getenv("RTDL_DB_PORT"))
	if err != nil {
		db_port_env = 0
	}

	if db_host_env != "" {
		db_host = db_host_env
	}
	if db_port_env != 0 {
		db_port = db_port_env
	}
	if db_user_env != "" {
		db_user = db_user_env
	}
	if db_password_env != "" {
		db_password = db_password_env
	}
	if db_dbname_env != "" {
		db_dbname = db_dbname_env
	}
	psqlCon = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", db_host, db_port, db_user, db_password, db_dbname)
}

//map between Go and Parquet data types
func getParquetDataType(dataType string) string {

	switch dataType {
	case "string":
		return `BYTE_ARRAY`
	case "int32":
		return `INT32`
	case "int64":
		return `INT64`
	case "int96":
		return `INT96`
	case "float32":
		return `FLOAT`
	case "float64":
		return `DOUBLE`

	}
	return ""

}

//use reflection to study incoming generic payload and construct schema necessary for Parquet
//payload is passed recursively through the function to break down till the elemental level

func generateSchema(payload map[string]interface{}, messageType string, jsonSchema string) string {

	if jsonSchema == "" {
		jsonSchema = `{"Tag": "name=` + messageType + `, repetitiontype=REQUIRED",`
		jsonSchema += `"Fields": [`
	}

	for key, value := range payload {
	
	
		if value == nil {
		
			continue //skip nulls
		}

		dataType := reflect.TypeOf(value).String()

		//special processing for nested object structures
		if strings.HasPrefix(dataType, "map[string]interface") {
		
			if len(value.(map[string]interface{})) == 0 {
			
				continue //skip empty structs
			
			}
		
		
			jsonSchema += `{"Tag": "name=` + key


			jsonSchema += `, repetitiontype=REQUIRED", "Fields" : [`
			jsonSchema = generateSchema(value.(map[string]interface{}), messageType, jsonSchema) //need recursion
			jsonSchema = strings.TrimRight(jsonSchema, ",")                                      //remove trailing comma
			jsonSchema += `]},`

		} else if strings.HasPrefix(dataType, "[]interface") { //special processing for arrays as well
		
		
			if len(value.([]interface{})) > 0 { //to be generated only for non-empty arrays

				jsonSchema += `{"Tag": "name=` + key

		
				jsonSchema += `, type=LIST, repetitiontype=REQUIRED", "Fields" : [`
				arrayItemDataType := reflect.TypeOf(value.([]interface{})[0]).String()
				if strings.HasPrefix(arrayItemDataType, "map[string]interface") { //if array consists of objects then same have to be recursed
					jsonSchema += `{"Tag": "name=element, repetitiontype=REQUIRED", "Fields" : [`
					jsonSchema = generateSchema(value.([]interface{})[0].(map[string]interface{}), messageType, jsonSchema)					
					jsonSchema = strings.TrimRight(jsonSchema, ",")
					jsonSchema += `]},`
				} else { //arrays composed of native data types can be handled directly
					jsonSchema += `{"Tag": "name=element, type=` + getParquetDataType(reflect.TypeOf(value.([]interface{})[0]).String())
					jsonSchema += `, repetitiontype=REQUIRED"},`
				}
				jsonSchema = strings.TrimRight(jsonSchema, ",")
				jsonSchema += `]},`
			
			}
			

		} else { //native data type
		
			jsonSchema += `{"Tag": "name=` + key


			jsonSchema += `, type=` + getParquetDataType(dataType)
			jsonSchema += `, repetitiontype=REQUIRED"},`

		}

	}	
	
	return jsonSchema

}

func generateSubFolderName(messageType string, configRecord Config) string {

	var subFolderName string

	for _, partitionTimeRecord := range partitionTimes { //need to find out the write partition

		if partitionTimeRecord.PartitionTimeId == configRecord.PartitionTimeId.Int64 { //match found

			switch partitionTimeRecord.PartitionTimeName {

			case "Hourly":

				subFolderName = messageType + "/" + time.Now().Format("2006-01-02-15")

			case "Daily":
				subFolderName = messageType + "/" + time.Now().Format("2006-01-02")

			case "Weekly":
				year, week := time.Now().ISOWeek()
				subFolderName = messageType + "/" + strconv.Itoa(year) + "-" + strconv.Itoa(week)

			case "Monthly":
				subFolderName = messageType + "/" + time.Now().Format("2006-01")

			case "Quarterly":
				quarter := int((time.Now().Month() + 2) / 3)
				subFolderName = messageType + "/" + time.Now().Format("2006") + "-" + string(quarter)
			}

		}

	}

	return subFolderName
}

//generate the leaf level file name
func generateLeafLevelFileName() string {

	//construct the timestamp string
	t := time.Now()
	year := t.Year()
	month := t.Month()
	day := t.Day()
	hour := t.Hour()
	min := t.Minute()
	sec := t.Second()
	nanosec := t.Nanosecond()

	return strconv.Itoa(year) + strconv.Itoa(int(month)) + strconv.Itoa(day) + "_" + strconv.Itoa(hour) + strconv.Itoa(min) + strconv.Itoa(sec) + strconv.Itoa(nanosec) + ".parquet"

}

//writer-agnostic function to actually write to file
func WriteToFile(schema string, fw source.ParquetFile, payload []byte, configRecord Config) error {

	//log.Println("Schema : ", schema)

	pw, err := writer.NewJSONWriter(schema, fw, 4)
	if err != nil {
		log.Println("Can't create json writer", err)
		return err
	}
	
	//set compression
	
	compressionType := configRecord.CompressionTypeId.Int64

	if compressionType > 0 && compressionType < 4 { //supported compression type
		
			switch compressionType {
			case 1:
				pw.CompressionType = parquet.CompressionCodec_SNAPPY
			case 2:
				pw.CompressionType = parquet.CompressionCodec_GZIP
			case 3:
				pw.CompressionType = parquet.CompressionCodec_LZO				
			
			} 
	}


	if err = pw.Write(payload); err != nil {
		log.Println("Write error", err)
		return err
	}

	if err = pw.WriteStop(); err != nil {
		log.Println("WriteStop error", err)
		return err
	}
	log.Println("Write Finished")
	fw.Close()
	return nil

}

//Function for making Dremio entry
func UpdateDremio (messageType string, sourceType string, location string, configRecord Config) error {

	var sourceDef []byte
	var sourceExists bool 
	var sourceId string
	var datasetExists bool

	//desiredPath := messageType + "_" + sourceType //our source names will be <message type>_<source type>
	sourceName := configRecord.StreamId.String
	dremioResponse, err1 := DremioReqRes("source",nil)
	
	if err1 != nil {
	
		log.Println("Error retrieving Dremio catalog information ", err1)
		return err1
	
	}

	log.Println("Dremio source information retrieved")
	
	//iterate through catalog and find if space already exists
	
	sources, ok1 := dremioResponse["data"].([] interface {})
	if !ok1 {
	
		return errors.New("error handling Dremio server response during source retrieval")
	}


	for _, source := range sources {
	
		entry, ok2 := source.(map[string] interface {})
		if !ok2 {
		
			return errors.New("error handling Dremio server response during source retrieval")
		}
		
		if entry["name"] == sourceName {

			sourceExists = true

			//ok, source exits - check if dataset exits
			sourceId, _ = entry["id"].(string)
			dremioResponse, _ = DremioReqRes("catalog/"+sourceId, nil)

			children, ok3 := dremioResponse["children"].([] interface {})
			if !ok3 {
			
				return errors.New("error handling Dremio server response during dataset retrieval ok3")
			}

			for _, childNode := range children {

				child, _ := childNode.(map[string] interface{})

				path, ok4 := child["path"].([] interface {}) 
				if !ok4 {
			
					return errors.New("error handling Dremio server response during dataset retrieval ok4")
				}

				datasetName, _ := path[1].(string)
				datasetType, _ := child["type"].(string)
				if datasetName == messageType && datasetType == "DATASET" {

					datasetExists = true
					break
				}
			}
			break
		}		
	}

	
	
	if !sourceExists {

		log.Println("Source does not exist for message type, creating ...")
		dremioMountPath := GetEnv("DREMIO_MOUNT_PATH","/mnt/datastore")
		
		sourceStringMultiLine := `{"name": "` + sourceName + `"`	
		switch sourceType {
		
		case "Local":
			sourceStringMultiLine += `, "type": "NAS", "config": {"path": "file:///` + dremioMountPath + `/` + configRecord.FolderName.String 
			
		case "S3":
			
			sourceStringMultiLine+= `, "type": "S3", "config": {"accessKey": "` + configRecord.AWSAcessKeyID.String + `"`
			sourceStringMultiLine += `, "accessSecret": "` + configRecord.AWSSecretAcessKey.String + `"`
			//sourceStringMultiLine += `, "externalBucketList": ["` + location + `"]`
			sourceStringMultiLine += `, "rootPath": "/` + location + `/`
			if configRecord.FolderName.String != "" {
				sourceStringMultiLine += configRecord.FolderName.String + `/` 
				
			}
			
		case "GCS":
			var gcpCreds map[string] interface {}
			//need to extract all variable values from GCP crendentials file
			
			err := json.Unmarshal([]byte(configRecord.GCPJsonCredentials.String), &gcpCreds)
			if err != nil {
				log.Println("Error reading GCP credentials from configuration record", err)
				return err
			}
			
			projectId := gcpCreds["project_id"].(string)
			clientEmail := gcpCreds["client_email"].(string)
			clientId := gcpCreds["client_id"].(string)
			privateKeyId := gcpCreds["private_key_id"].(string)
			privateKey := strings.Replace(gcpCreds["private_key"].(string),"\n","\\n",-1)
			sourceStringMultiLine+= `, "type":"GCS", "config": {"projectId": "` + projectId  + `"`
			sourceStringMultiLine+= `, "authMode": "SERVICE_ACCOUNT_KEYS", "clientEmail": "` + clientEmail + `"`
			sourceStringMultiLine+= `, "clientId": "` + clientId + `", "privateKeyId": "` + privateKeyId + `"`
			sourceStringMultiLine+= `, "privateKey": "` + privateKey + `"`
			sourceStringMultiLine+= `, "rootPath": "/` + location + `/`
			if configRecord.FolderName.String != "" {
				sourceStringMultiLine += configRecord.FolderName.String + `/` 
				
			}
		}
		
		sourceStringMultiLine += `"}}`
		
		
		
		sourceDef = []byte(sourceStringMultiLine)
		
		dremioResponse, err1 = DremioReqRes("source", sourceDef)
		
		
		if err1 != nil {
		
			log.Println("Error creating Dremio source ", err1)
			return err1
		}

	}


	if !datasetExists {

		
		//next we have to create the dataset

		encodedId := "dremio%3A%2F"+sourceName+"%2F"+messageType

		datasetDefMultiLine := `{"id": "` + encodedId + `", "entityType": "dataset", "path": ["` + sourceName + `", "` + messageType + `"]`
		
		datasetDefMultiLine += `, "format": {"type": "Parquet"}`
		datasetDefMultiLine += `, "type": "PHYSICAL_DATASET"`
		datasetDefMultiLine += `}`
		datasetDef := []byte(datasetDefMultiLine)

		dremioResponse, err1 = DremioReqRes("catalog/"+encodedId, datasetDef)
		
		if err1 != nil {
		
			log.Println("Error creating Dremio dataset ", err1)
			return err1
		}

	}
	
	return nil

}


//Write local Parquet
func WriteLocalParquet(messageType string, schema string, payload []byte, configRecord Config) error {

	//write
	path := "datastore" //root will always be datastore
	
	folderName := configRecord.FolderName.String
	if folderName != "" { //default
		path += "/" + folderName
	}

	path += "/" + generateSubFolderName(messageType, configRecord)

	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		log.Println("Can't create output directory", err)
		return err
	}

	location := os.Getenv("LOCAL_FS_MOUNT_PATH") + "/" + path
	fileName := path + "/" + generateLeafLevelFileName()
	
	log.Println("Local path:", fileName)

	fw, err := local.NewLocalFileWriter(fileName)

	if err != nil {
		log.Println("Can't create file", err)
		return err
	}


	err = WriteToFile(schema, fw, payload, configRecord)
	
	if err == nil { //file write successful, update Dremio
	
		return UpdateDremio(messageType,"Local", location, configRecord)
	
	}
	
	return err

}

func WriteAWSParquet(messageType string, schema string, payload []byte, configRecord Config) error {

	var key string	
	

	subFolderName := generateSubFolderName(messageType, configRecord)
	leafLevelFileName := generateLeafLevelFileName()

	if configRecord.Region.String == "" {
		return errors.New("AWS Region cannot be null or empty")
	}

	region := strings.TrimSpace(configRecord.Region.String)
	awsAccessKeyId := strings.TrimSpace(configRecord.AWSAcessKeyID.String)
	awsSecretAccessKey := strings.TrimSpace(configRecord.AWSSecretAcessKey.String)

	//log.Println("AWS Parquet writing implementation pending")
	bucketName := configRecord.BucketName.String
	if bucketName == "" {
		return errors.New("S3 bucket name cannot be null or empty")
	}

	if configRecord.FolderName.String != "" {

		key = configRecord.FolderName.String + "/" + subFolderName + "/" + leafLevelFileName
		

	} else {

		key = subFolderName + "/" + leafLevelFileName
		
	}

	fw, err := local.NewLocalFileWriter(leafLevelFileName)
	err = WriteToFile(schema, fw, payload, configRecord) //write temporary local file
	if err != nil {
		log.Println("Unable to write temporary local file", err)
		return err
	}

	awsSession, err := session.NewSession(&aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(awsAccessKeyId, awsSecretAccessKey, ""),
	})
	if err != nil {
		log.Println("Failed to create AWS Session ", err)
		return err
	}

	tempFile, err1 := os.Open(leafLevelFileName) //open temporary local file
	if err1 != nil {
		log.Println("Unable to open temporary local file", err1)
		return err1
	}

	defer tempFile.Close()

	// Get file size and read the file content into a buffer
	fileInfo, _ := tempFile.Stat()
	var size int64 = fileInfo.Size()
	buffer := make([]byte, size)
	tempFile.Read(buffer)

	// Config settings: this is where we choose the bucket, filename, content-type etc.
	// of the file we're uploading.
	_, err = s3.New(awsSession).PutObject(&s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		//ACL:                  aws.String("private"),
		Body: bytes.NewReader(buffer),
		//ContentLength:        aws.Int64(size),
		//ContentType:          aws.String(http.DetectContentType(buffer)),
		//ContentDisposition:   aws.String("attachment"),
		//ServerSideEncryption: aws.String("AES256"),
	})
	

	os.Remove(leafLevelFileName) //remove the temp file
	log.Println("Finished uploading file to S3")
	
	if err == nil {
	
		return UpdateDremio(messageType,"S3", bucketName, configRecord)
	} 
	
	
	return err

}

func WriteGCPParquet(messageType string, schema string, payload []byte, configRecord Config) error {

	var path string
	//var location string

	subFolderName := generateSubFolderName(messageType, configRecord)
	leafLevelFileName := generateLeafLevelFileName()

	
	
	//replace all \n	with \\n to preserve them
	jsonCreds := strings.Replace(configRecord.GCPJsonCredentials.String, "\n", "\\n", -1)
	

	//create client
	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(ctx, []byte(jsonCreds), secretmanager.DefaultAuthScopes()...)
	if err != nil {
		log.Println("Error creating GCP credentials", err)
		return err
	}

	client, err := storage.NewClient(ctx, option.WithCredentials(creds))
	if err != nil {
		log.Println("Error creating GCP client", err)
		return err
	}
	defer client.Close()

	bucketName := configRecord.BucketName.String
	if bucketName == "" {
		return errors.New("GCS bucket name cannot be null or empty")
	}

	if configRecord.FolderName.String != "" {

		path = configRecord.FolderName.String + "/" + subFolderName + "/" + leafLevelFileName
		//location = configRecord.FolderName.String + "/" + subFolderName
	} else {

		path = subFolderName + "/" + leafLevelFileName
		//location = subFolderName
	}

	fw, err := local.NewLocalFileWriter(leafLevelFileName)
	err = WriteToFile(schema, fw, payload, configRecord) //write temporary local file
	if err != nil {
		log.Println("Unable to write temporary local file", err)
		return err
	}

	tempFile, err1 := os.Open(leafLevelFileName) //open temporary local file
	if err1 != nil {
		log.Println("Unable to open temporary local file", err1)
		return err1
	}

	defer tempFile.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*50)
	defer cancel()

	// Upload an object with storage.Writer.
	writer := client.Bucket(bucketName).Object(path).NewWriter(ctx)
	if _, err = io.Copy(writer, tempFile); err != nil {
		log.Println("Error uploading file", err)
		return err
	}
	if err := writer.Close(); err != nil {
		log.Println("Error closing writer", err)
		return err
	}

	os.Remove(leafLevelFileName) //remove the temp file


	log.Println("Finished uploading file to GCS")
	return UpdateDremio(messageType,"GCS",bucketName, configRecord)
	
}

//Parquet writing logic
func writeParquet(request IncomingMessage) error {

	//log.Println(generateSchema(request.Payload,request.MessageType, "")+"]}")
	
	//message type precedence order will be 1."type" within request.Payload 2."message_type" within incoming message 3. Config Record MessageType
	//a default value will also be kept
	
	var messageType string = "rtdl_default"
	
	payload, _ := json.Marshal(request.Payload) //convert generic payload structure to JSON string
	
	var matchingConfig Config

	//first retrieve relevant destination information from config array

	for _, configRecord := range configs {
					
		if request.StreamAltId != "" { //use stream_alt_id
		
			if configRecord.StreamAltId.String == request.StreamAltId {
			
				matchingConfig = configRecord
				break
				
			}
		
		} else if request.StreamId != "" {
		
			
			if configRecord.StreamId.String == request.StreamId {
			
				matchingConfig = configRecord
				break
				
			}
			
		
		}
	
	
	}
		
	//least precendence - config record message_type
	if matchingConfig.MessageType.String != "" {
	
		messageType = matchingConfig.MessageType.String
	} 

	//higher precendence message_type within message
	if request.MessageType != "" {

		messageType = request.MessageType
	}
	
	
	//highest precedence - type inside main payload
	if payloadType, found := request.Payload["type"]; found {
		if typeString, ok := payloadType.(string); ok {
		
			messageType = typeString
		}
	}
	
	schema := strings.TrimRight(generateSchema(request.Payload, messageType, ""), ",") + "]}"

	//log.Println(schema)

	for _, fileStoreTypeRecord := range fileStoreTypes { //similar logic for file store types

		if fileStoreTypeRecord.FileStoreTypeId == matchingConfig.FileStoreTypeId.Int64 {

			switch fileStoreTypeRecord.FileStoreTypeName {
			case "Local":
				return WriteLocalParquet(messageType, schema, payload, matchingConfig)
			case "AWS":
				return WriteAWSParquet(messageType, schema, payload, matchingConfig)
			case "GCP":
				return WriteGCPParquet(messageType, schema, payload, matchingConfig)

			}
		}

	}
	return nil
}

//main stateful function
func Ingest(ctx statefun.Context, message statefun.Message) error {
	var request IncomingMessage
	if err := message.As(IncomingMessageType, &request); err != nil {
		return fmt.Errorf("failed to deserialize incoming message: %w", err)
	}

	if request.MessageType == "rtdl_205" { //this is internal message for refershing configuration cache

		err := LoadConfig()

		if err != nil {
			log.Println(err)
			return err
		}

		return nil
	}

	err := writeParquet(request)
	if err != nil {

		log.Println("error writing Parquet", err)

	}

	payload, _ := json.Marshal(request.Payload) //convert generic payload structure to JSON string

	//initial implementation to test out data flow
	//not required once actual Parquet writing logic has been implemented
	ctx.SendEgress(statefun.KafkaEgressBuilder{
		Target: KafkaEgressTypeName,
		Topic:  "egress",
		Key:    "message",
		Value:  []byte(payload),
	})

	log.Println("egress message written")

	return nil
}


func main() {

	//log.Println(net.LookupHost("host.docker.internal"))
	// connection string
	SetDBConnectionString()

	//load configuration at the outset
	//should panic if unable to do source
	err := LoadConfig()

	if err != nil {
		log.Fatal("Unable to load configuration ", err)
	}
	
	err = SetDremioConnection()

	if err != nil {
	
		log.Fatal("Unable to connect with Dremio ", err)
	}

	builder := statefun.StatefulFunctionsBuilder()

	//only the one function in the chain now
	_ = builder.WithSpec(statefun.StatefulFunctionSpec{
		FunctionType: IngestTypeName,
		Function:     statefun.StatefulFunctionPointer(Ingest),
	})

	http.Handle("/statefun", builder.AsHandler())
	_ = http.ListenAndServe(":8082", nil)
}
