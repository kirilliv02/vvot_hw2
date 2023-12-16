package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/urfave/cli/v2"
	"gopkg.in/ini.v1"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type Client struct {
	*s3.Client
	bucket string
}

func main() {
	var (
		path  string
		album string
		photo string
	)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("не удалось получить домашний каталог")
	}

	configDir := filepath.Join(homeDir, ".config/cloudphoto.exe")
	err = os.MkdirAll(configDir, os.ModePerm)
	if err != nil {
		log.Fatal("не удалось создать каталог для конфига")
	}
	configPath := filepath.Join(configDir, "cloudphotorc")

	albumFlag := &cli.StringFlag{
		Required:    true,
		Aliases:     []string{"a"},
		Value:       "",
		Name:        "album",
		Usage:       "Название альбома",
		Destination: &album,
	}

	pathFlag := &cli.StringFlag{
		Name:        "path",
		Value:       ".",
		Aliases:     []string{"p"},
		Usage:       "Название каталога",
		Destination: &path,
	}

	app := &cli.App{
		Name:                 "cloudphoto.exe",
		Usage:                "Приложение \"Фотоархив\"",
		EnableBashCompletion: true,
		Commands: []*cli.Command{
			{
				Name:    "upload",
				Aliases: []string{"u"},
				Usage:   "Отправка фотографий в облачное хранилище",
				Action: func(cCtx *cli.Context) error {
					client, err := initClient(configPath)
					if err != nil {
						return err
					}
					files, err := getImages(path)

					if err != nil {
						return err
					}

					uploadImages(client, files, album)

					return nil
				},
				Flags: []cli.Flag{albumFlag, pathFlag},
			},
			{
				Name:    "download",
				Aliases: []string{"d"},
				Usage:   "Загрузка фотографий из облачного хранилища",
				Action: func(cCtx *cli.Context) error {
					client, err := initClient(configPath)

					if err != nil {
						return err
					}

					return downloadImages(client, album, path)
				},
				Flags: []cli.Flag{albumFlag, pathFlag},
			},
			{
				Name:    "list",
				Aliases: []string{"l"},
				Usage:   "Вывод списка альбомов и фотографий в облачном хранилище",
				Action: func(cCtx *cli.Context) error {
					client, err := initClient(configPath)
					if err != nil {
						return err
					}

					return list(client, album)
				},
				Flags: []cli.Flag{&cli.StringFlag{
					Aliases:     []string{"a"},
					Value:       "",
					Name:        "album",
					Usage:       "Название альбома",
					Destination: &album,
				}},
			},
			{
				Name:    "delete",
				Aliases: []string{"del"},
				Usage:   "Удаление альбомов и фотографий в облачном хранилище",
				Action: func(cCtx *cli.Context) error {
					client, err := initClient(configPath)
					if err != nil {
						return err
					}

					return deleteImageOrAlbum(client, album, photo)
				},
				Flags: []cli.Flag{albumFlag,
					&cli.StringFlag{
						Aliases:     []string{"p"},
						Name:        "photo",
						Value:       "",
						Usage:       "Название каталога",
						Destination: &photo,
					},
				},
			},
			{
				Name:    "mksite",
				Aliases: []string{"mk"},
				Usage:   "Формирование и публикация веб-страниц фотоархива",
				Action: func(cCtx *cli.Context) error {
					client, err := initClient(configPath)
					if err != nil {
						return err
					}
					return makeSite(client)
				},
			},
			{
				Name:    "init",
				Aliases: []string{"i"},
				Usage:   "Инициализация программы",
				Action: func(cCtx *cli.Context) error {
					return initCloudPhoto(configPath)
				},
			},
		},
		Action: func(cCtx *cli.Context) error {
			fmt.Println("Пожалуйста укажите команду. Чтобы узнать какие команды поддерживаются используйте --help")
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func initCloudPhoto(configPath string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("Введите aws_access_key_id: ")
	scanner.Scan()
	accessKey := scanner.Text()
	fmt.Print("Введите aws_secret_access_key: ")
	scanner.Scan()
	secretKey := scanner.Text()
	fmt.Print("Введите bucket: ")
	scanner.Scan()
	bucket := scanner.Text()

	inidata := ini.Empty()
	sec, _ := inidata.NewSection("DEFAULT")
	sec.NewKey("bucket", bucket)
	sec.NewKey("aws_access_key_id", accessKey)
	sec.NewKey("aws_secret_access_key", secretKey)
	sec.NewKey("region", "ru-central1")
	sec.NewKey("endpoint_url", "https://storage.yandexcloud.net")

	err := inidata.SaveTo(configPath)
	if err != nil {
		return fmt.Errorf("не удалось сохрнаить файл конфигурации")
	}

	client, _ := initClient(configPath)

	if ok, err := isBucketExist(client, bucket); !ok {
		if err != nil {
			os.Remove(configPath)
			return fmt.Errorf("неверные ключи")
		}
		err := createBucket(client, bucket)
		if err != nil {
			return err
		}
	}

	fmt.Printf("Конфиг сохранен по пути: %s\n", configPath)

	return nil
}

func deleteImageOrAlbum(client *Client, album string, photo string) error {
	if photo == "" {
		v2, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket: aws.String(client.bucket),
			Prefix: aws.String(album + "/"),
		})
		if err != nil {
			return err
		}
		if len(v2.Contents) == 0 {
			return fmt.Errorf("нет заданного альбома")
		}

		for _, content := range v2.Contents {
			_, err := client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
				Bucket: aws.String(client.bucket),
				Key:    content.Key,
			})

			if err != nil {
				return err
			}
		}
	} else {
		file := filepath.ToSlash(filepath.Join(album, photo))
		_, err := client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: aws.String(client.bucket),
			Key:    aws.String(file),
		})

		if err != nil {
			return err
		}
	}
	return nil
}

func list(client *Client, album string) error {
	if album == "" {
		v2, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket: aws.String(client.bucket),
		})
		if err != nil {
			return err
		}

		if len(v2.Contents) == 0 {
			return fmt.Errorf("нет альбомов")
		}

		m := make(map[string]struct{})
		for _, content := range v2.Contents {
			if strings.Contains(*content.Key, "/") {
				m[strings.Split(*content.Key, "/")[0]] = struct{}{}
			}
		}
		for k := range m {
			fmt.Println(k)
		}
	} else {
		v2, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket: aws.String(client.bucket),
			Prefix: aws.String(album + "/"),
		})
		if err != nil {
			return err
		}

		if len(v2.Contents) == 0 {
			return fmt.Errorf("нет заданного альбома")
		}
		for _, content := range v2.Contents {
			fmt.Println(strings.Split(*content.Key, "/")[1])
		}
	}
	return nil
}

//go:embed templates/*
var content embed.FS

func initClient(filePath string) (*Client, error) {
	inidata, err := ini.Load(filePath)
	if err != nil {
		return nil, fmt.Errorf("не найден конфиг")
	}
	section := inidata.Section("DEFAULT")

	bucket := section.Key("bucket").String()
	accessKey := section.Key("aws_access_key_id").String()
	secretKey := section.Key("aws_secret_access_key").String()
	region := section.Key("region").String()
	endpoint := section.Key("endpoint_url").String()

	cfg := config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
		accessKey,
		secretKey,
		""))

	defaultConfig, _ := config.LoadDefaultConfig(context.Background(), cfg)

	defaultConfig.Region = region
	defaultConfig.BaseEndpoint = &endpoint

	client := s3.NewFromConfig(defaultConfig)

	return &Client{
		Client: client,
		bucket: bucket,
	}, nil
}

func downloadImages(client *Client, album string, path string) error {
	v2, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket: aws.String(client.bucket),
		Prefix: aws.String(album + "/"),
	})
	if err != nil {
		return err
	}

	if len(v2.Contents) == 0 {
		return fmt.Errorf("нет альбома: %s", album)
	}

	for _, content := range v2.Contents {
		object, err := client.GetObject(context.Background(), &s3.GetObjectInput{
			Bucket: &client.bucket,
			Key:    content.Key,
		})
		if err != nil {
			return err
		}
		path := filepath.Join(path, filepath.Base(*content.Key))
		file, _ := os.Create(path)
		_, err = file.ReadFrom(object.Body)
		if err != nil {
			return fmt.Errorf("ошибка записи в директорию")
		}
		file.Close()
	}
	return nil
}

func isBucketExist(client *Client, bucketName string) (bool, error) {
	buckets, err := client.ListBuckets(context.Background(), &s3.ListBucketsInput{})
	if err != nil {
		return false, err
	}
	for _, bucket := range buckets.Buckets {
		if *bucket.Name == bucketName {
			return true, nil
		}
	}

	return false, nil
}

func createBucket(client *Client, bucketName string) error {
	_, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: &bucketName,
	})

	return err
}

func makeSite(client *Client) error {
	client.PutBucketAcl(context.Background(), &s3.PutBucketAclInput{
		Bucket: aws.String(client.bucket),
		ACL:    types.BucketCannedACLPublicRead,
	})

	client.PutBucketWebsite(context.Background(), &s3.PutBucketWebsiteInput{
		Bucket: aws.String(client.bucket),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			ErrorDocument: &types.ErrorDocument{Key: aws.String("error.html")},
			IndexDocument: &types.IndexDocument{Suffix: aws.String("index.html")},
		},
	})

	v2, err := client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket: aws.String(client.bucket),
	})
	if err != nil {
		return err
	}

	albums := make(map[string][]string)

	for _, content := range v2.Contents {
		path := *content.Key

		if strings.Contains(path, "/") {
			album := strings.Split(path, "/")[0]
			file := strings.Split(path, "/")[1]
			albums[album] = append(albums[album], file)
		}

	}
	url := fmt.Sprintf("https://%s.website.yandexcloud.net/", client.bucket)
	number := 1
	indexData := make([]struct {
		File string
		Name string
	}, 0)

	for k, v := range albums {
		indexData = append(indexData, struct {
			File string
			Name string
		}{File: fmt.Sprintf("album%d.html", number), Name: k})

		albumData := make([]struct {
			Url  string
			Name string
		}, 0)
		for _, fileName := range v {
			albumData = append(albumData, struct {
				Url  string
				Name string
			}{Url: fmt.Sprintf("%s%s/%s", url, k, fileName), Name: fileName})
		}

		albumsTmpl, err := template.ParseFS(content, "templates/album.html")
		if err != nil {
			return err
		}
		var bs []byte
		file := bytes.NewBuffer(bs)

		err = albumsTmpl.Execute(file, albumData)
		if err != nil {
			return err
		}

		client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket:      aws.String(client.bucket),
			Key:         aws.String(fmt.Sprintf("album%d.html", number)),
			Body:        file,
			ContentType: aws.String("text/html; charset=utf-8"),
		})

		number++
	}

	indexTmpl, err := template.ParseFS(content, "templates/index.html")
	if err != nil {
		return err
	}

	var bs []byte
	file := bytes.NewBuffer(bs)

	err = indexTmpl.Execute(file, indexData)
	if err != nil {
		return err
	}

	client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(client.bucket),
		Key:         aws.String("index.html"),
		Body:        file,
		ContentType: aws.String("text/html; charset=utf-8"),
	})

	errorTemplate, err := content.Open("templates/error.html")

	if err != nil {
		return err
	}

	defer errorTemplate.Close()

	client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(client.bucket),
		Key:         aws.String("error.html"),
		Body:        errorTemplate,
		ContentType: aws.String("text/html; charset=utf-8"),
	})

	fmt.Println(url)
	return nil
}

func getImages(path string) ([]string, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения директории: %s", err)
	}

	filePaths := make([]string, 0, 10)

	for _, file := range files {
		if !file.IsDir() &&
			(strings.HasSuffix(file.Name(), ".jpg") || strings.HasSuffix(file.Name(), ".jpeg")) {
			filePaths = append(filePaths, filepath.Join(path, file.Name()))
		}
	}
	if len(filePaths) == 0 {
		return nil, fmt.Errorf("в директории нет картинок")
	}
	return filePaths, nil
}

func uploadImages(client *Client, files []string, album string) {
	for _, filePath := range files {
		file, _ := os.Open(filePath)

		_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket:      &client.bucket,
			Key:         aws.String(filepath.ToSlash(filepath.Join(album, filepath.Base(filePath)))),
			Body:        file,
			ContentType: aws.String("image/jpeg"),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ошибка отправки картинки: %s", filepath.Base(filePath))
		}
	}
	return

}
