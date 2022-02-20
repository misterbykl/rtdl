# rtdl - The Real-Time Data Lake ⚡️
<img src="./public/logos/rtdl-logo.png" height="250px" width="250px"></img>  
[![MIT License](https://img.shields.io/apm/l/atomic-design-ui.svg?)](https://github.com/tterb/atomic-design-ui/blob/master/LICENSES)  
rtdl makes it easy to build and maintain a real-time data lake. You configure a data stream 
with a source (from a tool like Segment) and a cloud storage destination, and rtdl builds you 
a real-time data lake in Parquet format that automatically works with [Dremio](https://www.dremio.com/) 
to give you access your real-time data in common BI and ML tools – just like a data warehouse.  
  
You provide the streams, rtdl builds your data lake.


## V0.0.2 - Current status -- what works and what doesn't

### What works? 🚀
rtdl is not full-featured yet, but it is currently functional. You can use the API on port 80 to 
configure streams that ingest json from an rtdl endpoint on port 8080, process them into Parquet, 
and save the files to a destination configured in your stream. rtdl can write files locally, to 
AWS S3, and to GCP Cloud Storage, and you can query your data with Dremio on port 9047 (login with 
Username: `rtdl` and Password `rtdl1234`).

### What's new? 💥
  * Switched from Apache Hive Metastore + Presto to Dremio. **Dremio works for all storage types.**
  * Added support for using a flattened JSON object as value for `gcp_json_credentials` field in the 
    `createStream` API call. Previously, you had to double-quote everything and flatten.
  * Added CONTRIBUTING.md and decided to use a DCO over a CLA - tl;dr use -s when you commit, like 
    `git commit -s -m "..."`
  * Added support for Azure Blob Storage V2 (please note that for events written to Azure Blob Storage
    V2 - it can take time up to 1 minute for data to reflect in Dremio)
  * Added support for GZIP and LZO compressions in addition to SNAPPY (default). Specify `compression_type_id`
    as 2 for GZIP and 3 for LZO
  


### What doesn't work/what's next on the roadmap? 🚴🏼  
  * Segment webhook support
  * Writing to HDFS
  * User Interface for Stream creation
  

## Quickstart 🌱
### Initialize the rtdl services
1.  Run `docker compose -f docker-compose.init.yml up -d`.
    * **Note:** This configuration should be fault-tolerant, but if any containers or 
      processes fail when running this, run `docker compose -f docker-compose.init.yml down` 
      and retry.
2.  After the containers `rtdl_rtdl-db-init` and `rtdl_dremio-init` exit and complete with `EXITED (0)`, kill and 
    delete the rtdl container set by running `docker compose -f docker-compose.init.yml down`.
3.  Run `docker compose up -d` every time after.  
    **Note:** Your memory setting in Docker must be at least 8GB. rtdl may become unstable if it is 
    set lower.
    * `docker compose down` to stop.

### Interact with rtdl services and create a data lake
All API calls used to interact with rtdl have Postman examples in our [postman-rtdl-public repo](https://github.com/realtimedatalake/postman-rtdl-public).
1.  If you are building your data lake on a cloud vendor's storage service, configure your storage 
    buckets and access:
    * For AWS S3, follow the [Segment docs for AWS S3](https://segment.com/docs/connections/storage/catalog/aws-s3/). 
      You will need your bucket name, your AWS access key id, and your AWS secret access key.
      * For your IAM setup, you can use the below policy:
        ```
        {
            "Version": "2012-10-17",
            "Statement": [
                {
                    "Sid": "ListAllBuckets",
                    "Effect": "Allow",
                    "Action": [
                        "s3:GetBucketLocation",
                        "s3:ListAllMyBuckets"
                    ],
                    "Resource": [
                        "arn:aws:s3:::*"
                    ]
                },
                {
                    "Sid": "ListBucket",
                    "Effect": "Allow",
                    "Action": [
                        "s3:ListBucket"
                    ],
                    "Resource": [
                        "arn:aws:s3:::rtdl-test-bucket-aws"
                    ]
                },
                {
                    "Sid": "ManageBucket",
                    "Effect": "Allow",
                    "Action": [
                        "s3:GetObject",
                        "s3:PutObject",
                        "s3:PutObjectAcl",
                        "s3:DeleteObject"
                    ],
                    "Resource": [
                        "arn:aws:s3:::rtdl-test-bucket-aws/*"
                    ]
                }
            ]
        }
        ```
    * For GCP Cloud Storage, follow the [Segment docs for Google Cloud Storage](https://segment.com/docs/connections/storage/catalog/google-cloud-storage/). 
      Instead of giving your service account object-level access as described in Segment's 
      documentation, make your service account a Principal in IAM and give it `Storage Admin` access.
      * You will need your credentials in flattened json (remove all the newlines).
3.  Instrument your website with [analytics-next-cc](https://github.com/realtimedatalake/analytics-next-cc) - 
    our fork of [Segment's Analytics.js 2.0](https://segment.com/docs/connections/sources/catalog/libraries/website/javascript/) 
    that lets you cc all of the events you send to Segment to rtdl's ingest endpoint. Its 
    snippet is a drop-in replacement of Analytics.js 2.0/Analytics.js. Using this makes it 
    easy to build your data lake with existing Segment instrumentation. Enter your ingest endpoint
    as the `ccUrl` value and rtdl will handle the payload. Make sure you enter your writeKey in the 
    `stream_alt_id` of your `stream` configuration (below).
    * Alternatively, you can send ***any*** json with just ```stream_id``` in the payload and rtdl will add it to your lake.
      ```
      {
          "stream_id":"837a8d07-cd06-4e17-bcd8-aef0b5e48d31",
          "name":"user1",
          "array":[1,2,3],
          "properties":{"age":20}
      }
      ```
	You can optionally add ```message_type``` should you choose to override the ```message_type``` specified while creating the stream.
	rtdl will default to a message type ```rtdl_default``` if message type is absent in both stream definition and actual message
	
	
4.  Create/read/update/delete `stream` configurations that define a source data stream into 
    your data lake and the destination data store as well as configure folder partitioning and 
    file compression. It also allows for activating/deactivating a stream.
    * For any json data being sent to the ingest endpoint, the generated `stream_id` or the 
      manually input `stream_alt_id` values are required in the payload.

**Note:** To start from scratch, run `rm -rf storage/` from the rtdl root folder.


## Architecture 🏛
rtdl has a multi-service architecture composed of tested and trusted open source tools 
to process and catalog your data and custom-built services to interact with them more easily.

### config services

#### config
API service written in Go. Use the API to create, read, update, activate, deactivate, 
and delete `stream` records. `stream` records store the configuration information for 
the different data streams you want to send to your data lake. This service can also be 
used to lookup master data necessary for creating successful `stream` records like 
`file_store_types`, `partition_times`, and `compression_types`.  
**Environment Variables:** RTDL_DB_HOST, RTDL_DB_USER, RTDL_DB_PASSWORD, RTDL_DB_DBNAME  
**Public Port:** 80  
**Endpoints:**
  * /getStream -- POST; `stream_id` required
  * /getAllStreams -- GET
  * /getAllActiveStreams -- GET
  * /createStream -- POST; `message_type` and `folder_name` required
  * /updateStream -- PUT; all fields required (any missing fields will be replaced with NULL 
    values)
  * /deleteStream -- DELETE; `stream_id` required
  * /activateStream -- PUT; `stream_id` required
  * /deactivateStream -- PUT; `stream_id` required
  * /getAllFileStoreTypes -- GET
  * /getAllPartitionTimes -- GET
  * /getAllCompressionTypes -- GET
  
  Sample ```createStream``` payload for creating Parquet file in AWS S3
  ```	
  {
	"active": true,
    "message_type": "test-msg-aws",
	"file_store_type_id": 2,
	"region": "us-east-1",
	"bucket_name": "testBucketAWS",
	"folder_name": "testFolderAWS",
    "partition_time_id": 1,
    "compression_type_id": 1,
	"aws_access_key_id": "[aws_access_key_id]",
    "aws_secret_access_key": "[aws_secret_access_key]"
  }
  ```
  
  ```file_store_type_id``` - 1 for Local, 2 for AWS, 3 for GCS
  ```partiion_time_id``` - 1 - HOURLY, 2 - DAILY, 3 - WEEKLY, 4 - MONTHLY, 5 - QAURTERLY
  
  For cloud storage - final file path would be 
  ```<bucket>/<folder>/<message type>/<time partition>/*.parquet```
  
  ```time partition``` part can look like 
	```2021-06-15-13```(Hourly), 
	```2021-06-15```(Daily),
	```2021-48```(Weekly - ISOWeek), 
	```2021-06```(Monthly),
	```2021-02```(Quarterly)
	
  The leaf-level file would have timestamp upto milliseconds as the file name
  

#### rtdl-db
YugabyteDB or PostgreSQL (both configurations included in the docker compose files). This service 
stores the `stream` configuration data written by the `config` service and read by the `ingester` 
stateful function  
  * **Database Name:** rtdl_db
  * **Username:** rtdl
  * **Password:** rtdl

**Tables**
  * file_store_types
    * file_store_type_id SERIAL,
    * file_store_type_name VARCHAR,
    * PRIMARY KEY (file_store_type_id)
  * partition_times
    * partition_time_id SERIAL,
    * partition_time_name VARCHAR,
    * PRIMARY KEY (partition_time_id)
  * compression_types
    * compression_type_id SERIAL,
    * compression_type_name VARCHAR,
    * PRIMARY KEY (compression_type_id)
  * streams
    * stream_id uuid DEFAULT gen_random_uuid(),
    * stream_alt_id VARCHAR,
    * active BOOLEAN DEFAULT FALSE,
    * message_type VARCHAR NOT NULL,
    * file_store_type_id INTEGER DEFAULT 1,
    * region VARCHAR,
    * bucket_name VARCHAR,
    * folder_name VARCHAR NOT NULL,
    * partition_time_id INTEGER DEFAULT 1,
    * compression_type_id INTEGER DEFAULT 1,
    * aws_access_key_id VARCHAR,
    * aws_secret_access_key VARCHAR,
    * gcp_json_credentials VARCHAR,
    * created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    * updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    * PRIMARY KEY (stream_id),
    * FOREIGN KEY(file_store_type_id) REFERENCES file_store_types(file_store_type_id),
    * FOREIGN KEY(partition_time_id) REFERENCES partition_times(partition_time_id),
    * FOREIGN KEY(compression_type_id) REFERENCES compression_types(compression_type_id)

### ingest service
Service written in Go that accepts a JSON payload and writes it to Kafka for processing by the 
`ingester` stateful function. 
**Public Port:** 8080  
**Endpoints:**
  * /ingest -- POST; accepts JSON payload along with a write key
  * /refreshCache -- GET; triggers a refresh of the streams cache in the `ingester` stateful function

### kafka services
Standard Kafka services. Creates data streams that can be read by a Stateful Function. Images from Bitnami.
  * kafka-zookeeper - Apache Zookeeper service
  * kafka - Apache Kafka service

### process services
Apache Flink [Stateful Functions](https://flink.apache.org/stateful-functions.html) cluster in a standard 
configuration – a job manager service with paired task manager and stateful function services.
  * statefun-manager - Apache Flink Stateful Functions manager service  
    **Public Port:** 8081  
  * statefun-worker - Apache Flink Stateful Functions task manager service
  * statefun-functions - Apache Flink Stateful function written in Go named `ingester`. Reads JSON 
    payloads posted to Kafka, processes and stores the data in Parque format based on the configuration 
    in the associated streams record.  
    **Public Port:** 8082  
    **Environment Variables:** RTDL_DB_HOST, RTDL_DB_USER, RTDL_DB_PASSWORD, RTDL_DB_DBNAME, DREMIO_HOST, 
    DREMIO_PORT, DREMIO_USERNAME, DREMIO_PASSWORD, DREMIO_MOUNT_PATH


### dremio service
Standard Dremio service. Dremio makes the data in your data lake accessible in real-time. Dremio makes it easy 
to discover, curate, accelerate, and share data. Use port 9047 to access Dremio's UI and query your data.
**Public Ports:** 9047, 31010, 45678  
  * dremio - Dremio's [dremio-oss](https://github.com/dremio/dremio-oss) service


## License 🤝
[MIT](./LICENSE)


## Contributing 😎
Contributions are always welcome!  
See our [CONTRIBUTING](./CONTRIBUTING.md) for ways to get started. 
This project adheres to the rtdl [code of conduct](./CODE_OF_CONDUCT.md) - a 
direct adaptation of the [Contributor Covenant](https://www.contributor-covenant.org/), 
version [2.1](https://www.contributor-covenant.org/version/2/1/code_of_conduct.html).


## Appreciation 🙏
  * [Apache Flink](https://flink.apache.org/)
  * [Flink Stateful Functions](https://flink.apache.org/stateful-functions.html)
  * [Dremio](https://www.dremio.com/)
  * [Apache Kafka](https://kafka.apache.org/)
  * [sqlx](https://github.com/jmoiron/sqlx) by [jmoiron](https://github.com/jmoiron)
  * [parquet-go](https://github.com/xitongsys/parquet-go) by [xitongsys](https://github.com/xitongsys)
  * [kafka-go](https://github.com/segmentio/kafka-go) by [Segment](https://github.com/segmentio)


## Authors ✍🏽
  * [Dipanjan Biswas](https://www.github.com/dipanjanb)
  * [Gavin Johnson](https://www.github.com/thtmnisamnstr)
