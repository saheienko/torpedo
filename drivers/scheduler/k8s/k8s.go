package k8s

import (
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/portworx/torpedo/drivers/scheduler"
	"github.com/portworx/torpedo/drivers/scheduler/k8s/spec"
	"github.com/portworx/torpedo/drivers/scheduler/k8s/spec/factory"
	"github.com/portworx/torpedo/pkg/k8sutils"
	"k8s.io/client-go/pkg/api/v1"
	storage_v1beta1 "k8s.io/client-go/pkg/apis/storage/v1beta1"
	// blank importing all applications specs to allow them to init()
	_ "github.com/portworx/torpedo/drivers/scheduler/k8s/spec/postgres"
	"k8s.io/client-go/pkg/apis/apps/v1beta1"
)

// SchedName is the name of the kubernetes scheduler driver implementation
const SchedName = "k8s"

type k8s struct {
	nodes []scheduler.Node
}

func (k *k8s) GetNodes() []scheduler.Node {
	return k.nodes
}

// String returns the string name of this driver.
func (k *k8s) String() string {
	return SchedName
}

func (k *k8s) Init() error {
	nodes, err := k8sutils.GetNodes()
	if err != nil {
		return err
	}

	for _, n := range nodes.Items {
		var addrs []string
		for _, addr := range n.Status.Addresses {
			if addr.Type == v1.NodeExternalIP || addr.Type == v1.NodeInternalIP {
				addrs = append(addrs, addr.Address)
			}
		}

		var nodeType scheduler.NodeType
		if k8sutils.IsNodeMaster(n) {
			nodeType = scheduler.NodeTypeMaster
		} else {
			nodeType = scheduler.NodeTypeWorker
		}

		k.nodes = append(k.nodes, scheduler.Node{
			Name:      n.Name,
			Addresses: addrs,
			Type:      nodeType,
		})
	}

	return nil
}

func (k *k8s) Schedule(instanceID string, options scheduler.ScheduleOptions) ([]*scheduler.Context, error) {
	var specs []spec.AppSpec
	if options.AppKeys != nil && len(options.AppKeys) > 0 {
		for _, key := range options.AppKeys {
			spec, err := factory.Get(key)
			if err != nil {
				return nil, err
			}
			specs = append(specs, spec)
		}
	} else {
		specs = factory.GetAll()
	}

	var contexts []*scheduler.Context
	for _, spec := range specs {
		for _, storage := range spec.Storage(instanceID) {
			if obj, ok := storage.(*storage_v1beta1.StorageClass); ok {
				sc, err := k8sutils.CreateStorageClass(obj)
				if err != nil {
					return nil, &ErrFailedToScheduleApp{
						App:   spec,
						Cause: fmt.Sprintf("Failed to create storage class: %v. Err: %v", obj.Name, err),
					}
				}
				logrus.Printf("Created storage class: %v", sc)
			} else if obj, ok := storage.(*v1.PersistentVolumeClaim); ok {
				pvc, err := k8sutils.CreatePersistentVolumeClaim(obj)
				if err != nil {
					return nil, &ErrFailedToScheduleApp{
						App:   spec,
						Cause: fmt.Sprintf("Failed to create PVC: %v. Err: %v", obj.Name, err),
					}
				}
				logrus.Printf("Created PVC: %v", pvc)
			} else {
				return nil, &ErrFailedToScheduleApp{
					App:   spec,
					Cause: fmt.Sprintf("Failed to create unsupported storage component: %#v.", storage),
				}
			}
		}

		for _, core := range spec.Core(instanceID) {
			if obj, ok := core.(*v1beta1.Deployment); ok {
				dep, err := k8sutils.CreateDeployment(obj)
				if err != nil {
					return nil, &ErrFailedToScheduleApp{
						App:   spec,
						Cause: fmt.Sprintf("Failed to create Deployment: %v. Err: %v", obj.Name, err),
					}
				}
				logrus.Printf("Created deployment: %v", dep)
			} else {
				return nil, &ErrFailedToScheduleApp{
					App:   spec,
					Cause: fmt.Sprintf("Failed to create unsupported core component: %#v.", core),
				}
			}
		}

		ctx := &scheduler.Context{
			UID: instanceID,
			App: spec,
			// Status: TODO
			// Stdout: TODO
			// Stderr: TODO
		}

		contexts = append(contexts, ctx)
	}

	return contexts, nil
}

func (k *k8s) WaitForRunning(ctx *scheduler.Context) error {
	for _, core := range ctx.App.Core(ctx.UID) {
		if obj, ok := core.(*v1beta1.Deployment); ok {
			if err := k8sutils.ValidateDeployement(obj); err != nil {
				return &ErrFailedToValidateApp{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to validate Deployment: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Validated deployment: %v", obj.Name)
		} else {
			return &ErrFailedToValidateApp{
				App:   ctx.App,
				Cause: fmt.Sprintf("Failed to validate unsupported core component: %#v.", core),
			}
		}
	}

	return nil
}

func (k *k8s) Destroy(ctx *scheduler.Context) error {
	for _, core := range ctx.App.Core(ctx.UID) {
		if obj, ok := core.(*v1beta1.Deployment); ok {
			if err := k8sutils.DeleteDeployment(obj); err != nil {
				return &ErrFailedToDestroyApp{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to destroy Deployment: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Destroyed deployment: %v", obj.Name)
		} else {
			return &ErrFailedToDestroyApp{
				App:   ctx.App,
				Cause: fmt.Sprintf("Failed to destroy unsupported core component: %#v.", core),
			}
		}
	}

	return nil
}

func (k *k8s) WaitForDestroy(ctx *scheduler.Context) error {
	for _, core := range ctx.App.Core(ctx.UID) {
		if obj, ok := core.(*v1beta1.Deployment); ok {
			if err := k8sutils.ValidateTerminatedDeployment(obj); err != nil {
				return &ErrFailedToValidateAppDestroy{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to validate destroy of deployment: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Validated destroy of deployment: %v", obj.Name)
		} else {
			return &ErrFailedToValidateAppDestroy{
				App:   ctx.App,
				Cause: fmt.Sprintf("Failed to validate destory of unsupported core component: %#v.", core),
			}
		}
	}
	return nil
}

func (k *k8s) GetVolumes(ctx *scheduler.Context) ([]string, error) {
	var volumes []string
	for _, storage := range ctx.App.Storage(ctx.UID) {
		if obj, ok := storage.(*v1.PersistentVolumeClaim); ok {
			vol, err := k8sutils.GetVolumeForPersistentVolumeClaim(obj)
			if err != nil {
				return nil, &ErrFailedToGetVolumesForApp{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to get volume for PVC: %v. Err: %v", obj.Name, err),
				}
			}

			volumes = append(volumes, vol)
		}
	}

	return volumes, nil
}

func (k *k8s) GetVolumeParameters(ctx *scheduler.Context) (map[string]map[string]string, error) {
	result := make(map[string]map[string]string)

	for _, storage := range ctx.App.Storage(ctx.UID) {
		if obj, ok := storage.(*v1.PersistentVolumeClaim); ok {
			vol, err := k8sutils.GetVolumeForPersistentVolumeClaim(obj)
			if err != nil {
				return nil, &ErrFailedToGetVolumesParameters{
					App:   ctx.App,
					Cause: fmt.Sprintf("failed to get volume for PVC: %v. Err: %v", obj.Name, err),
				}
			}

			params, err := k8sutils.GetPersistentVolumeClaimParams(obj)
			if err != nil {
				return nil, &ErrFailedToGetVolumesParameters{
					App:   ctx.App,
					Cause: fmt.Sprintf("failed to get params for volume: %v. Err: %v", obj.Name, err),
				}
			}
			result[vol] = params
		}
	}

	return result, nil
}

func (k *k8s) InspectVolumes(ctx *scheduler.Context) error {
	for _, storage := range ctx.App.Storage(ctx.UID) {
		if obj, ok := storage.(*storage_v1beta1.StorageClass); ok {
			if err := k8sutils.ValidateStorageClass(obj); err != nil {
				return &ErrFailedToValidateStorage{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to validate StorageClass: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Validated storage class: %v", obj.Name)
		} else if obj, ok := storage.(*v1.PersistentVolumeClaim); ok {
			if err := k8sutils.ValidatePersistentVolumeClaim(obj); err != nil {
				return &ErrFailedToValidateStorage{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to validate PVC: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Validated PVC: %v", obj.Name)
		} else {
			return &ErrFailedToValidateStorage{
				App:   ctx.App,
				Cause: fmt.Sprintf("Failed to validate unsupported storage component: %#v.", storage),
			}
		}
	}

	return nil
}

func (k *k8s) DeleteVolumes(ctx *scheduler.Context) error {
	for _, storage := range ctx.App.Storage(ctx.UID) {
		if obj, ok := storage.(*storage_v1beta1.StorageClass); ok {
			if err := k8sutils.DeleteStorageClass(obj); err != nil {
				return &ErrFailedToDestroyStorage{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to destroy storage class: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Destroyed storage class: %v", obj.Name)
		} else if obj, ok := storage.(*v1.PersistentVolumeClaim); ok {
			if err := k8sutils.DeletePersistentVolumeClaim(obj); err != nil {
				return &ErrFailedToDestroyStorage{
					App:   ctx.App,
					Cause: fmt.Sprintf("Failed to destroy PVC: %v. Err: %v", obj.Name, err),
				}
			}
			logrus.Printf("Destroyed PVC: %v", obj.Name)
		} else {
			return &ErrFailedToDestroyStorage{
				App:   ctx.App,
				Cause: fmt.Sprintf("Failed to destroy unsupported storage component: %#v.", storage),
			}
		}
	}

	return nil
}

func init() {
	k := &k8s{}
	scheduler.Register(SchedName, k)
}
