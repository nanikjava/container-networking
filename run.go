package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func createContainerID() string {
	randBytes := make([]byte, 6)
	rand.Read(randBytes)
	return fmt.Sprintf("%02x%02x%02x%02x%02x%02x",
						randBytes[0], randBytes[1], randBytes[2],
						randBytes[3], randBytes[4], randBytes[5])
}

func getContainerFSHome(contanerID string) string {
	return getGockerContainersPath() + "/" + contanerID + "/fs"
}

func createContainerDirectories(containerID string) {
	contHome := getGockerContainersPath() + "/" + containerID
	contDirs := []string{contHome + "/fs", contHome + "/fs/mnt", contHome + "/fs/upperdir", contHome + "/fs/workdir"}
	if err := createDirsIfDontExist(contDirs); err != nil {
		log.Fatalf("Unable to create required directories: %v\n", err)
	}
}

func mountOverlayFileSystem(containerID string, imageShaHex string) {
	var srcLayers []string
	pathManifest :=  getManifestPathForImage(imageShaHex)
	mani := manifest{}
	parseManifest(pathManifest, &mani)
	if len(mani) == 0 || len(mani[0].Layers) == 0 {
		log.Fatal("Could not find any layers.")
	}
	if len(mani) > 1 {
		log.Fatal("I don't know how to handle more than one manifest.")
	}

	imageBasePath := getBasePathForImage(imageShaHex)
	for _, layer := range mani[0].Layers {
		srcLayers = append([]string{imageBasePath + "/" + layer[:12] + "/fs"}, srcLayers...)
		//srcLayers = append(srcLayers, imageBasePath + "/" + layer[:12] + "/fs")
	}
	contFSHome := getContainerFSHome(containerID)
	mntOptions := "lowerdir="+strings.Join(srcLayers, ":")+",upperdir="+contFSHome+"/upperdir,workdir="+contFSHome+"/workdir"
	if err:= syscall.Mount("none", contFSHome + "/mnt", "overlay", 0, mntOptions); err != nil {
		log.Fatalf("Mount failed: %v\n", err)
	}
}

/*
	Called if this program is executed with "child-mode" as the first argument
*/
func execContainerCommand(containerID string) {
	mntPath := getContainerFSHome(containerID) + "/mnt"
	cmd := exec.Command(os.Args[3], os.Args[4:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	doOrDieWithMsg(syscall.Sethostname([]byte(containerID)), "Unable to set hostname")
	doOrDieWithMsg(joinContainerNetworkNamespace(containerID), "Unable to join container network namespace")
	doOrDieWithMsg(syscall.Chroot(mntPath), "Unable to chroot")
	doOrDieWithMsg(os.Chdir("/"), "Unable to change directory")
	createDirsIfDontExist([]string{"/proc"})
	doOrDieWithMsg(syscall.Mount("proc", "/proc", "proc", 0, ""), "Unable to mount proc")
	doOrDieWithMsg(syscall.Mount("tmpfs", "/tmp", "tmpfs", 0, ""), "Unable to mount tmpfs")
	setupLocalInterface()
	cmd.Run()
	doOrDie(syscall.Unmount("/proc", 0))
	doOrDie(syscall.Unmount("/tmp", 0))
}

func spawnChild(containerID string) {

	/* Setup the network namespace  */
	cmd := &exec.Cmd{
		Path: "/proc/self/exe",
		Args: []string{"/proc/self/exe", "setup-netns", containerID},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd.Run()

	/* Setup the virtual interface  */
	cmd = &exec.Cmd{
		Path: "/proc/self/exe",
		Args: []string{"/proc/self/exe", "fence-veth", containerID},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd.Run()

	/* Setup the virtual interface  */
	cmd = &exec.Cmd{
		Path: "/proc/self/exe",
		Args: []string{"/proc/self/exe", "setup-veth", containerID},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	cmd.Run()

	args := append([]string{containerID}, os.Args[3:]...)
	args = append([]string{"child-mode"}, args...)
	cmd = exec.Command("/proc/self/exe", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS,
		Unshareflags: syscall.CLONE_NEWNS,
	}
	doOrDie(cmd.Run())
}

func initContainer(src string)  {
	containerID := createContainerID()
	log.Printf("New container ID: %s\n", containerID)
	imageShaHex := downloadImageIfRequired(src)
	log.Printf("Image to overlay mount: %s\n", imageShaHex)
	createContainerDirectories(containerID)
	mountOverlayFileSystem(containerID, imageShaHex)
	if err := setupVirtualEthOnHost(containerID); err != nil {
		log.Fatalf("Unable to setup Veth0 on host: %v", err)
	}
	spawnChild(containerID)
	log.Printf("Container done.\n")
	doOrDie(syscall.Unmount(getContainerFSHome(containerID) + "/mnt", 0))
}