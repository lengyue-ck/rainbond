// RAINBOND, Application Management Platform
// Copyright (C) 2014-2017 Goodrain Co., Ltd.

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. For any non-GPL usage of Rainbond,
// one or multiple Commercial Licenses authorized by Goodrain Co., Ltd.
// must be obtained first.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package exector

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/goodrain/rainbond/util"

	"github.com/goodrain/rainbond/builder/sources"
	"github.com/goodrain/rainbond/builder/sources/registry"
	"github.com/goodrain/rainbond/db"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/client"
	dbmodel "github.com/goodrain/rainbond/db/model"
	"github.com/goodrain/rainbond/event"
	"github.com/pquerna/ffjson/ffjson"
	"github.com/tidwall/gjson"
)

const (
	// OldMetadata identify older versions of metadata
	OldMetadata = "OldMetadata"
	// NewMetadata identify new version of metadata
	NewMetadata = "NewMetadata"
)

//maxBackupVersionSize Maximum number of backup versions per service
var maxBackupVersionSize = 3

//BackupAPPNew backup group app new version
type BackupAPPNew struct {
	GroupID    string   `json:"group_id" `
	ServiceIDs []string `json:"service_ids" `
	//full-online,full-offline
	Mode     string `json:"mode"`
	Version  string `json:"version"`
	EventID  string
	SlugInfo struct {
		Namespace   string `json:"namespace"`
		FTPHost     string `json:"ftp_host"`
		FTPPort     string `json:"ftp_port"`
		FTPUser     string `json:"ftp_username"`
		FTPPassword string `json:"ftp_password"`
	} `json:"slug_info"`
	ImageInfo struct {
		HubURL      string `json:"hub_url"`
		HubUser     string `json:"hub_user"`
		HubPassword string `json:"hub_password"`
		Namespace   string `json:"namespace"`
		IsTrust     bool   `json:"is_trust,omitempty"`
	} `json:"image_info,omitempty"`
	SourceDir    string `json:"source_dir"`
	SourceType   string `json:"source_type"`
	BackupID     string `json:"backup_id"`
	BackupSize   int64
	Logger       event.Logger
	DockerClient *client.Client
}

func init() {
	RegisterWorker("backup_apps_new", BackupAPPNewCreater)
}

//BackupAPPNewCreater create
func BackupAPPNewCreater(in []byte, m *exectorManager) (TaskWorker, error) {
	eventID := gjson.GetBytes(in, "event_id").String()
	logger := event.GetManager().GetLogger(eventID)
	backupNew := &BackupAPPNew{
		Logger:       logger,
		EventID:      eventID,
		DockerClient: m.DockerClient,
	}
	if err := ffjson.Unmarshal(in, &backupNew); err != nil {
		return nil, err
	}
	return backupNew, nil
}

// AppSnapshot holds a snapshot of your app
type AppSnapshot struct {
	Services            []*RegionServiceSnapshot
	Plugins             []*dbmodel.TenantPlugin
	PluginBuildVersions []*dbmodel.TenantPluginBuildVersion
}

//RegionServiceSnapshot RegionServiceSnapshot
type RegionServiceSnapshot struct {
	ServiceID          string
	Service            *dbmodel.TenantServices
	ServiceProbe       []*dbmodel.TenantServiceProbe
	LBMappingPort      []*dbmodel.TenantServiceLBMappingPort
	ServiceEnv         []*dbmodel.TenantServiceEnvVar
	ServiceLabel       []*dbmodel.TenantServiceLable
	ServiceMntRelation []*dbmodel.TenantServiceMountRelation
	ServiceRelation    []*dbmodel.TenantServiceRelation
	ServiceStatus      string
	ServiceVolume      []*dbmodel.TenantServiceVolume
	ServicePort        []*dbmodel.TenantServicesPort
	Versions           []*dbmodel.VersionInfo

	PluginRelation    []*dbmodel.TenantServicePluginRelation
	PluginConfigs     []*dbmodel.TenantPluginVersionDiscoverConfig
	PluginEnvs        []*dbmodel.TenantPluginVersionEnv
	PluginStreamPorts []*dbmodel.TenantServicesStreamPluginPort
}

//Run Run
func (b *BackupAPPNew) Run(timeout time.Duration) error {
	//read region group app metadata
	metadata, err := ioutil.ReadFile(fmt.Sprintf("%s/region_apps_metadata.json", b.SourceDir))
	if err != nil {
		return err
	}

	metaVersion, err := b.judgeMetadataVersion(metadata)
	if err != nil {
		b.Logger.Info(fmt.Sprintf("Failed to judge the version of metadata"), map[string]string{"step": "backup_builder", "status": "failure"})
		return err
	}
	if metaVersion == OldMetadata {
		var svcSnapshot []*RegionServiceSnapshot
		if err := ffjson.Unmarshal(metadata, &svcSnapshot); err != nil {
			b.Logger.Info(fmt.Sprintf("Failed to unmarshal metadata into RegionServiceSnapshot"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
		if err := b.backupServiceInfo(svcSnapshot); err != nil {
			b.Logger.Info(fmt.Sprintf("Failed to backup metadata service info"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
	} else {
		var appSnapshot AppSnapshot
		if err := ffjson.Unmarshal(metadata, &appSnapshot); err != nil {
			b.Logger.Info(fmt.Sprintf("Failed to unmarshal metadata into AppSnapshot"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
		if err := b.backupServiceInfo(appSnapshot.Services); err != nil {
			b.Logger.Info(fmt.Sprintf("Failed to backup metadata service info"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
		if err := b.backupPluginInfo(&appSnapshot); err != nil {
			b.Logger.Info(fmt.Sprintf("Failed to backup metadata plugin info"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
	}

	if strings.HasSuffix(b.SourceDir, "/") {
		b.SourceDir = b.SourceDir[:len(b.SourceDir)-2]
	}
	if err := util.Zip(b.SourceDir, fmt.Sprintf("%s.zip", b.SourceDir)); err != nil {
		b.Logger.Info(fmt.Sprintf("Compressed backup metadata failed"), map[string]string{"step": "backup_builder", "status": "starting"})
		return err
	}
	b.BackupSize += util.GetFileSize(fmt.Sprintf("%s.zip", b.SourceDir))
	os.RemoveAll(b.SourceDir)
	b.SourceDir = fmt.Sprintf("%s.zip", b.SourceDir)
	//upload app backup data to online server(sftp) if mode is full-online
	if b.Mode == "full-online" && b.SlugInfo.FTPHost != "" && b.SlugInfo.FTPPort != "" {
		b.Logger.Info(fmt.Sprintf("Start uploading backup metadata to the cloud"), map[string]string{"step": "backup_builder", "status": "starting"})
		sFTPClient, err := sources.NewSFTPClient(b.SlugInfo.FTPUser, b.SlugInfo.FTPPassword, b.SlugInfo.FTPHost, b.SlugInfo.FTPPort)
		if err != nil {
			b.Logger.Error(util.Translation("create ftp client error"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
		defer sFTPClient.Close()
		dstDir := fmt.Sprintf("%s/backup/%s_%s/metadata_data.zip", b.SlugInfo.Namespace, b.GroupID, b.Version)
		if err := sFTPClient.PushFile(b.SourceDir, dstDir, b.Logger); err != nil {
			b.Logger.Error(util.Translation("push slug file to sftp server error"), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("push  slug file error when backup app , %s", err.Error())
			return err
		}
		//Statistical backup size
		os.Remove(b.SourceDir)
		b.SourceDir = dstDir
		b.SourceType = "sftp"
	}
	if err := b.updateBackupStatu("success"); err != nil {
		return err
	}
	return nil
}

// judging whether the metadata structure is old or new, the new version is v5.1.8 and later
func (b *BackupAPPNew) judgeMetadataVersion(metadata []byte) (string, error) {
	var appSnapshot AppSnapshot
	if err := ffjson.Unmarshal(metadata, &appSnapshot); err == nil {
		return NewMetadata, nil
	}

	var svcSnapshot []*RegionServiceSnapshot
	if err := ffjson.Unmarshal(metadata, &svcSnapshot); err == nil {
		return "", err
	}

	return OldMetadata, nil
}

func (b *BackupAPPNew) backupServiceInfo(serviceInfos []*RegionServiceSnapshot) error {
	for _, app := range serviceInfos {
		//backup app image or code slug file
		b.Logger.Info(fmt.Sprintf("Start backup Application(%s) runtime", app.Service.ServiceAlias), map[string]string{"step": "backup_builder", "status": "starting"})
		var backupVersionSize int
		for _, version := range app.Versions {
			if backupVersionSize >= maxBackupVersionSize {
				break
			}
			if version.DeliveredType == "slug" && version.FinalStatus == "success" {
				if ok, _ := b.checkVersionExist(version); !ok {
					version.FinalStatus = "lost"
					continue
				}
				if err := b.uploadSlug(app, version); err != nil {
					logrus.Errorf("upload app %s version %s slug file error.%s", app.Service.ServiceName, version.BuildVersion, err.Error())
				} else {
					backupVersionSize++
				}
			}
			if version.DeliveredType == "image" && version.FinalStatus == "success" {
				if ok, _ := b.checkVersionExist(version); !ok {
					version.FinalStatus = "lost"
					continue
				}
				if err := b.uploadImage(app, version); err != nil {
					logrus.Errorf("upload app %s version %s image error.%s", app.Service.ServiceName, version.BuildVersion, err.Error())
				} else {
					backupVersionSize++
				}
			}
		}
		if backupVersionSize == 0 {
			b.Logger.Error(fmt.Sprintf("Application(%s) Backup build version failure.", app.Service.ServiceAlias), map[string]string{"step": "backup_builder", "status": "success"})
			return fmt.Errorf("Application(%s) Backup build version failure", app.Service.ServiceAlias)
		}
		logrus.Infof("backup app %s %d version", app.Service.ServiceName, backupVersionSize)
		b.Logger.Info(fmt.Sprintf("Complete backup application (%s) runtime %d version", app.Service.ServiceAlias, backupVersionSize), map[string]string{"step": "backup_builder", "status": "success"})
		b.Logger.Info(fmt.Sprintf("Start backup application(%s) persistent data", app.Service.ServiceAlias), map[string]string{"step": "backup_builder", "status": "starting"})
		//backup app data,The overall data of the direct backup service
		if len(app.ServiceVolume) > 0 {
			dstDir := fmt.Sprintf("%s/data_%s/%s.zip", b.SourceDir, "all", "data")
			_, sharepath := GetVolumeDir()
			serviceVolumeData := path.Join(sharepath, "tenant", app.Service.TenantID, "service", app.Service.ServiceID)
			if !util.DirIsEmpty(serviceVolumeData) {
				if err := util.Zip(serviceVolumeData, dstDir); err != nil {
					logrus.Errorf("backup service(%s) volume data error.%s", app.ServiceID, err.Error())
					return err
				}
			}
		}
		for _, volume := range app.ServiceVolume {
			dstDir := fmt.Sprintf("%s/data_%s/%s.zip", b.SourceDir, app.ServiceID, strings.Replace(volume.VolumeName, "/", "", -1))
			hostPath := volume.HostPath
			if hostPath != "" && !util.DirIsEmpty(hostPath) {
				if err := util.Zip(hostPath, dstDir); err != nil {
					logrus.Errorf("backup service(%s) volume(%s) data error.%s", app.ServiceID, volume.VolumeName, err.Error())
					return err
				}
			}
		}
		b.Logger.Info(fmt.Sprintf("Complete backup application(%s) persistent data", app.Service.ServiceAlias), map[string]string{"step": "backup_builder", "status": "success"})
	}
	return nil
}

func (b *BackupAPPNew) backupPluginInfo(appSnapshot *AppSnapshot) error {
	b.Logger.Info(fmt.Sprintf("Start backup plugin"), map[string]string{"step": "backup_builder", "status": "starting"})
	for _, pv := range appSnapshot.PluginBuildVersions {
		dstDir := fmt.Sprintf("%s/plugin_%s/image_%s.tar", b.SourceDir, pv.PluginID, pv.DeployVersion)
		util.CheckAndCreateDir(filepath.Dir(dstDir))
		if _, err := sources.ImagePull(b.DockerClient, pv.BuildLocalImage, "", "", b.Logger, 20); err != nil {
			b.Logger.Error(fmt.Sprintf("plugin image: %s; failed to pull image", pv.BuildLocalImage), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("plugin image: %s; failed to pull image: %v", pv.BuildLocalImage, err)
			return err
		}
		if err := sources.ImageSave(b.DockerClient, pv.BuildLocalImage, dstDir, b.Logger); err != nil {
			b.Logger.Error(util.Translation("save image to local dir error"), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("plugin image: %s; failed to save image: %v", pv.BuildLocalImage, err)
			return err
		}
	}
	return nil
}

func (b *BackupAPPNew) checkVersionExist(version *dbmodel.VersionInfo) (bool, error) {
	if version.DeliveredType == "image" {
		imageInfo := sources.ImageNameHandle(version.DeliveredPath)
		reg, err := registry.NewInsecure(imageInfo.Host, "", "")
		if err != nil {
			logrus.Errorf("new registry client error %s", err.Error())
			return false, err
		}
		_, err = reg.Manifest(imageInfo.Name, imageInfo.Tag)
		if err != nil {
			logrus.Errorf("get image %s manifest info failure, it could be not exist", version.DeliveredPath)
			return false, err
		}
		return true, nil
	}
	if version.DeliveredType == "slug" {
		islugfile, err := os.Stat(version.DeliveredPath)
		if os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if islugfile.IsDir() {
			return false, nil
		}
		return true, nil
	}
	return false, fmt.Errorf("delivered type is invalid")
}
func (b *BackupAPPNew) uploadSlug(app *RegionServiceSnapshot, version *dbmodel.VersionInfo) error {
	if b.Mode == "full-online" && b.SlugInfo.FTPHost != "" && b.SlugInfo.FTPPort != "" {
		sFTPClient, err := sources.NewSFTPClient(b.SlugInfo.FTPUser, b.SlugInfo.FTPPassword, b.SlugInfo.FTPHost, b.SlugInfo.FTPPort)
		if err != nil {
			b.Logger.Error(util.Translation("create ftp client error"), map[string]string{"step": "backup_builder", "status": "failure"})
			return err
		}
		defer sFTPClient.Close()
		dstDir := fmt.Sprintf("%s/backup/%s_%s/app_%s/%s.tgz", b.SlugInfo.Namespace, b.GroupID, b.Version, app.ServiceID, version.BuildVersion)
		if err := sFTPClient.PushFile(version.DeliveredPath, dstDir, b.Logger); err != nil {
			b.Logger.Error(util.Translation("push slug file to sftp server error"), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("push  slug file error when backup app , %s", err.Error())
			return err
		}
		//Statistical backup size
		b.BackupSize += util.GetFileSize(version.DeliveredPath)
	} else {
		dstDir := fmt.Sprintf("%s/app_%s/slug_%s.tgz", b.SourceDir, app.ServiceID, version.BuildVersion)
		util.CheckAndCreateDir(filepath.Dir(dstDir))
		if err := sources.CopyFileWithProgress(version.DeliveredPath, dstDir, b.Logger); err != nil {
			b.Logger.Error(util.Translation("push slug file to local dir error"), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("copy slug file error when backup app, %s", err.Error())
			return err
		}
	}
	return nil
}

func (b *BackupAPPNew) uploadImage(app *RegionServiceSnapshot, version *dbmodel.VersionInfo) error {
	if b.Mode == "full-online" && b.ImageInfo.HubURL != "" {
		backupImage, err := version.CreateShareImage(b.ImageInfo.HubURL, b.ImageInfo.Namespace, fmt.Sprintf("%s_backup", b.Version))
		if err != nil {
			return fmt.Errorf("create backup image error %s", err)
		}

		info, err := sources.ImagePull(b.DockerClient, version.DeliveredPath, "", "", b.Logger, 10)
		if err != nil {
			return fmt.Errorf("pull image when backup error %s", err)
		}
		if err := sources.ImageTag(b.DockerClient, version.DeliveredPath, backupImage, b.Logger, 1); err != nil {
			return fmt.Errorf("change  image tag when backup error %s", err)
		}
		if b.ImageInfo.IsTrust {
			if err := sources.TrustedImagePush(b.DockerClient, backupImage, b.ImageInfo.HubUser, b.ImageInfo.HubPassword, b.Logger, 10); err != nil {
				b.Logger.Error(util.Translation("save image to hub error"), map[string]string{"step": "backup_builder", "status": "failure"})
				return fmt.Errorf("backup image push error %s", err)
			}
		} else {
			if err := sources.ImagePush(b.DockerClient, backupImage, b.ImageInfo.HubUser, b.ImageInfo.HubPassword, b.Logger, 10); err != nil {
				b.Logger.Error(util.Translation("save image to hub error"), map[string]string{"step": "backup_builder", "status": "failure"})
				return fmt.Errorf("backup image push error %s", err)
			}
		}
		b.BackupSize += info.Size
	} else {
		dstDir := fmt.Sprintf("%s/app_%s/image_%s.tar", b.SourceDir, app.ServiceID, version.BuildVersion)
		util.CheckAndCreateDir(filepath.Dir(dstDir))
		if err := sources.ImageSave(b.DockerClient, version.DeliveredPath, dstDir, b.Logger); err != nil {
			b.Logger.Error(util.Translation("save image to local dir error"), map[string]string{"step": "backup_builder", "status": "failure"})
			logrus.Errorf("save image(%s) to local dir error when backup app, %s", version.DeliveredPath, err.Error())
			return err
		}
	}
	return nil
}

//Stop stop
func (b *BackupAPPNew) Stop() error {
	return nil
}

//Name return worker name
func (b *BackupAPPNew) Name() string {
	return "backup_apps_new"
}

//GetLogger GetLogger
func (b *BackupAPPNew) GetLogger() event.Logger {
	return b.Logger
}

//ErrorCallBack if run error will callback
func (b *BackupAPPNew) ErrorCallBack(err error) {
	if err != nil {
		logrus.Errorf("backup group app failure %s", err)
		b.Logger.Error(util.Translation("backup group app failure"), map[string]string{"step": "callback", "status": "failure"})
		b.updateBackupStatu("failed")
	}
}

func (b *BackupAPPNew) updateBackupStatu(status string) error {
	backupstatus, err := db.GetManager().AppBackupDao().GetAppBackup(b.BackupID)
	if err != nil {
		logrus.Errorf("update backup group app history failure %s", err)
		return err
	}
	backupstatus.Status = status
	backupstatus.SourceDir = b.SourceDir
	backupstatus.SourceType = b.SourceType
	backupstatus.BuckupSize = int(b.BackupSize)
	return db.GetManager().AppBackupDao().UpdateModel(backupstatus)
}

//GetVolumeDir get volume path prifix
func GetVolumeDir() (string, string) {
	localPath := os.Getenv("LOCAL_DATA_PATH")
	sharePath := os.Getenv("SHARE_DATA_PATH")
	if localPath == "" {
		localPath = "/grlocaldata"
	}
	if sharePath == "" {
		sharePath = "/grdata"
	}
	return localPath, sharePath
}

//GetServiceType get service deploy type
func GetServiceType(labels []*dbmodel.TenantServiceLable) string {
	for _, l := range labels {
		if l.LabelKey == dbmodel.LabelKeyServiceType {
			return l.LabelValue
		}
	}
	return util.StatelessServiceType
}
