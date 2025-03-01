// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2014-2016 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package backend_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	. "gopkg.in/check.v1"

	"github.com/snapcore/snapd/boot/boottest"
	"github.com/snapcore/snapd/bootloader"
	"github.com/snapcore/snapd/bootloader/bootloadertest"
	"github.com/snapcore/snapd/dirs"
	"github.com/snapcore/snapd/osutil"
	"github.com/snapcore/snapd/overlord/snapstate/backend"
	"github.com/snapcore/snapd/progress"
	"github.com/snapcore/snapd/snap"
	"github.com/snapcore/snapd/snap/snaptest"
	"github.com/snapcore/snapd/systemd"
	"github.com/snapcore/snapd/testutil"
)

type setupSuite struct {
	testutil.BaseTest
	be                backend.Backend
	umount            *testutil.MockCmd
	systemctlRestorer func()
}

var _ = Suite(&setupSuite{})

func (s *setupSuite) SetUpTest(c *C) {
	s.BaseTest.SetUpTest(c)
	s.BaseTest.AddCleanup(snap.MockSanitizePlugsSlots(func(snapInfo *snap.Info) {}))

	dirs.SetRootDir(c.MkDir())

	// needed for system key generation
	restore := osutil.MockMountInfo("")
	s.AddCleanup(restore)

	err := os.MkdirAll(filepath.Join(dirs.GlobalRootDir, "etc", "systemd", "system", "multi-user.target.wants"), 0755)
	c.Assert(err, IsNil)

	s.systemctlRestorer = systemd.MockSystemctl(func(cmd ...string) ([]byte, error) {
		return []byte("ActiveState=inactive\n"), nil
	})

	s.umount = testutil.MockCommand(c, "umount", "")
}

func (s *setupSuite) TearDownTest(c *C) {
	s.BaseTest.TearDownTest(c)
	dirs.SetRootDir("")
	bootloader.Force(nil)
	s.umount.Restore()
	s.systemctlRestorer()
}

var (
	mockClassicDev    = boottest.MockDevice("")
	mockDev           = boottest.MockDevice("boot-snap")
	mockDevWithKernel = boottest.MockDevice("kernel")
)

func (s *setupSuite) TestSetupDoUndoSimple(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)

	si := snap.SideInfo{
		RealName: "hello",
		Revision: snap.R(14),
	}

	snapType, installRecord, err := s.be.SetupSnap(snapPath, "hello", &si, mockDev, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Check(snapType, Equals, snap.TypeApp)

	// after setup the snap file is in the right dir
	c.Assert(osutil.FileExists(filepath.Join(dirs.SnapBlobDir, "hello_14.snap")), Equals, true)

	// ensure the right unit is created
	mup := systemd.MountUnitPath(filepath.Join(dirs.StripRootDir(dirs.SnapMountDir), "hello/14"))
	c.Assert(mup, testutil.FileMatches, fmt.Sprintf("(?ms).*^Where=%s", filepath.Join(dirs.StripRootDir(dirs.SnapMountDir), "hello/14")))
	c.Assert(mup, testutil.FileMatches, "(?ms).*^What=/var/lib/snapd/snaps/hello_14.snap")

	minInfo := snap.MinimalPlaceInfo("hello", snap.R(14))
	// mount dir was created
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, true)

	// undo undoes the mount unit and the instdir creation
	err = s.be.UndoSetupSnap(minInfo, "app", nil, mockDev, progress.Null)
	c.Assert(err, IsNil)

	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, false)

	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, false)
}

func (s *setupSuite) TestSetupDoUndoInstance(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)

	si := snap.SideInfo{
		RealName: "hello",
		Revision: snap.R(14),
	}

	snapType, installRecord, err := s.be.SetupSnap(snapPath, "hello_instance", &si, mockDev, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Check(snapType, Equals, snap.TypeApp)

	// after setup the snap file is in the right dir
	c.Assert(osutil.FileExists(filepath.Join(dirs.SnapBlobDir, "hello_instance_14.snap")), Equals, true)

	// ensure the right unit is created
	mup := systemd.MountUnitPath(filepath.Join(dirs.StripRootDir(dirs.SnapMountDir), "hello_instance/14"))
	c.Assert(mup, testutil.FileMatches, fmt.Sprintf("(?ms).*^Where=%s", filepath.Join(dirs.StripRootDir(dirs.SnapMountDir), "hello_instance/14")))
	c.Assert(mup, testutil.FileMatches, "(?ms).*^What=/var/lib/snapd/snaps/hello_instance_14.snap")

	minInfo := snap.MinimalPlaceInfo("hello_instance", snap.R(14))
	// mount dir was created
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, true)

	// undo undoes the mount unit and the instdir creation
	err = s.be.UndoSetupSnap(minInfo, "app", nil, mockDev, progress.Null)
	c.Assert(err, IsNil)

	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, false)

	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, false)
}

func (s *setupSuite) TestSetupDoUndoKernel(c *C) {
	bloader := bootloadertest.Mock("mock", c.MkDir())
	bootloader.Force(bloader)

	// we don't get real mounting
	os.Setenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS", "1")
	defer os.Unsetenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS")

	testFiles := [][]string{
		{"kernel.img", "kernel"},
		{"initrd.img", "initrd"},
		{"modules/4.4.0-14-generic/foo.ko", "a module"},
		{"firmware/bar.bin", "some firmware"},
		{"meta/kernel.yaml", "version: 4.2"},
	}
	snapPath := snaptest.MakeTestSnapWithFiles(c, `name: kernel
version: 1.0
type: kernel
`, testFiles)

	si := snap.SideInfo{
		RealName: "kernel",
		Revision: snap.R(140),
	}

	snapType, installRecord, err := s.be.SetupSnap(snapPath, "kernel", &si, mockDevWithKernel, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Check(snapType, Equals, snap.TypeKernel)
	c.Assert(installRecord, NotNil)
	c.Assert(bloader.ExtractKernelAssetsCalls, HasLen, 1)
	c.Assert(bloader.ExtractKernelAssetsCalls[0].InstanceName(), Equals, "kernel")
	minInfo := snap.MinimalPlaceInfo("kernel", snap.R(140))

	// undo deletes the kernel assets again
	err = s.be.UndoSetupSnap(minInfo, "kernel", nil, mockDevWithKernel, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(bloader.RemoveKernelAssetsCalls, HasLen, 1)
	c.Assert(bloader.RemoveKernelAssetsCalls[0].InstanceName(), Equals, "kernel")
}

func (s *setupSuite) TestSetupDoIdempotent(c *C) {
	// make sure that a retry wouldn't stumble on partial work
	// use a kernel because that does and need to do strictly more

	// this cannot check systemd own behavior though around mounts!

	bloader := bootloadertest.Mock("mock", c.MkDir())
	bootloader.Force(bloader)
	// we don't get real mounting
	os.Setenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS", "1")
	defer os.Unsetenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS")

	testFiles := [][]string{
		{"kernel.img", "kernel"},
		{"initrd.img", "initrd"},
		{"modules/4.4.0-14-generic/foo.ko", "a module"},
		{"firmware/bar.bin", "some firmware"},
		{"meta/kernel.yaml", "version: 4.2"},
	}
	snapPath := snaptest.MakeTestSnapWithFiles(c, `name: kernel
version: 1.0
type: kernel
`, testFiles)

	si := snap.SideInfo{
		RealName: "kernel",
		Revision: snap.R(140),
	}

	_, installRecord, err := s.be.SetupSnap(snapPath, "kernel", &si, mockDevWithKernel, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Assert(bloader.ExtractKernelAssetsCalls, HasLen, 1)
	c.Assert(bloader.ExtractKernelAssetsCalls[0].InstanceName(), Equals, "kernel")

	// retry run
	_, installRecord, err = s.be.SetupSnap(snapPath, "kernel", &si, mockDevWithKernel, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Assert(bloader.ExtractKernelAssetsCalls, HasLen, 2)
	c.Assert(bloader.ExtractKernelAssetsCalls[1].InstanceName(), Equals, "kernel")
	minInfo := snap.MinimalPlaceInfo("kernel", snap.R(140))

	// validity checks
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 1)
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, true)

	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, true)
}

func (s *setupSuite) TestSetupUndoIdempotent(c *C) {
	// make sure that a retry wouldn't stumble on partial work
	// use a kernel because that does and need to do strictly more

	// this cannot check systemd own behavior though around mounts!

	bloader := bootloadertest.Mock("mock", c.MkDir())
	bootloader.Force(bloader)
	// we don't get real mounting
	os.Setenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS", "1")
	defer os.Unsetenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS")

	testFiles := [][]string{
		{"kernel.img", "kernel"},
		{"initrd.img", "initrd"},
		{"modules/4.4.0-14-generic/foo.ko", "a module"},
		{"firmware/bar.bin", "some firmware"},
		{"meta/kernel.yaml", "version: 4.2"},
	}
	snapPath := snaptest.MakeTestSnapWithFiles(c, `name: kernel
version: 1.0
type: kernel
`, testFiles)

	si := snap.SideInfo{
		RealName: "kernel",
		Revision: snap.R(140),
	}

	_, installRecord, err := s.be.SetupSnap(snapPath, "kernel", &si, mockDevWithKernel, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)

	minInfo := snap.MinimalPlaceInfo("kernel", snap.R(140))

	err = s.be.UndoSetupSnap(minInfo, "kernel", nil, mockDevWithKernel, progress.Null)
	c.Assert(err, IsNil)

	// retry run
	err = s.be.UndoSetupSnap(minInfo, "kernel", nil, mockDevWithKernel, progress.Null)
	c.Assert(err, IsNil)

	// validity checks
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, false)

	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, false)

	// assets got extracted and then removed again
	c.Assert(bloader.ExtractKernelAssetsCalls, HasLen, 1)
	c.Assert(bloader.RemoveKernelAssetsCalls, HasLen, 1)
}

func (s *setupSuite) TestSetupUndoKeepsTargetSnapIfSymlink(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)
	c.Assert(os.MkdirAll(dirs.SnapBlobDir, 0755), IsNil)
	// symlink the test snap under target blob dir where SetupSnap would normally
	// install it, so that it realizes there is nothing to do.
	tmpPath := filepath.Join(dirs.SnapBlobDir, "hello_14.snap")
	c.Assert(os.Symlink(snapPath, tmpPath), IsNil)

	si := snap.SideInfo{RealName: "hello", Revision: snap.R(14)}
	_, installRecord, err := s.be.SetupSnap(snapPath, "hello", &si, mockDev, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Check(installRecord.TargetSnapExisted, Equals, true)

	minInfo := snap.MinimalPlaceInfo("hello", snap.R(14))

	// after setup the snap file is in the right dir
	c.Assert(osutil.FileExists(filepath.Join(dirs.SnapBlobDir, "hello_14.snap")), Equals, true)
	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, true)
	// validity
	c.Assert(osutil.IsSymlink(minInfo.MountFile()), Equals, true)

	// undo keeps the target .snap file intact if requested
	installRecord = &backend.InstallRecord{TargetSnapExisted: true}
	c.Assert(s.be.UndoSetupSnap(minInfo, "app", installRecord, mockDev, progress.Null), IsNil)
	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, true)
}

func (s *setupSuite) TestSetupUndoKeepsTargetSnapIgnoredIfNotSymlink(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)
	c.Assert(os.MkdirAll(dirs.SnapBlobDir, 0755), IsNil)
	// copy test snap to target blob dir where SetupSnap would normally install it,
	// so that it realizes there is nothing to do.
	tmpPath := filepath.Join(dirs.SnapBlobDir, "hello_14.snap")
	c.Assert(osutil.CopyFile(snapPath, tmpPath, 0), IsNil)

	si := snap.SideInfo{RealName: "hello", Revision: snap.R(14)}
	_, installRecord, err := s.be.SetupSnap(snapPath, "hello", &si, mockDev, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Check(installRecord.TargetSnapExisted, Equals, true)

	minInfo := snap.MinimalPlaceInfo("hello", snap.R(14))

	// after setup the snap file is in the right dir
	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, true)

	installRecord = &backend.InstallRecord{TargetSnapExisted: true}
	c.Assert(s.be.UndoSetupSnap(minInfo, "app", installRecord, mockDev, progress.Null), IsNil)
	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, false)
}

func (s *setupSuite) TestSetupCleanupAfterFail(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)

	si := snap.SideInfo{
		RealName: "hello",
		Revision: snap.R(14),
	}

	r := systemd.MockSystemctl(func(cmd ...string) ([]byte, error) {
		// mount unit start fails
		if len(cmd) >= 2 && cmd[0] == "reload-or-restart" && strings.HasSuffix(cmd[1], ".mount") {
			return nil, fmt.Errorf("failed")
		}
		return []byte("ActiveState=inactive\n"), nil
	})
	defer r()

	_, installRecord, err := s.be.SetupSnap(snapPath, "hello", &si, mockDev, nil, progress.Null)
	c.Assert(err, ErrorMatches, "failed")
	c.Check(installRecord, IsNil)

	// everything is gone
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Check(l, HasLen, 0)

	minInfo := snap.MinimalPlaceInfo("hello", snap.R(14))
	c.Check(osutil.FileExists(minInfo.MountDir()), Equals, false)
	c.Check(osutil.FileExists(minInfo.MountFile()), Equals, false)
	c.Check(osutil.FileExists(filepath.Join(dirs.SnapBlobDir, "hello_14.snap")), Equals, false)
}

func (s *setupSuite) TestRemoveSnapFilesDir(c *C) {
	snapPath := makeTestSnap(c, helloYaml1)

	si := snap.SideInfo{
		RealName: "hello",
		Revision: snap.R(14),
	}

	snapType, installRecord, err := s.be.SetupSnap(snapPath, "hello_instance", &si, mockDev, nil, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)
	c.Check(snapType, Equals, snap.TypeApp)

	minInfo := snap.MinimalPlaceInfo("hello_instance", snap.R(14))
	// mount dir was created
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, true)

	installRecord = &backend.InstallRecord{}
	s.be.RemoveSnapFiles(minInfo, snapType, installRecord, mockDev, progress.Null)
	c.Assert(err, IsNil)

	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(minInfo.MountDir()), Equals, false)
	c.Assert(osutil.FileExists(minInfo.MountFile()), Equals, false)
	c.Assert(osutil.FileExists(snap.BaseDir(minInfo.InstanceName())), Equals, true)
	c.Assert(osutil.FileExists(snap.BaseDir(minInfo.SnapName())), Equals, true)

	// /snap/hello is kept as other instances exist
	err = s.be.RemoveSnapDir(minInfo, true)
	c.Assert(err, IsNil)
	c.Assert(osutil.FileExists(snap.BaseDir(minInfo.InstanceName())), Equals, false)
	c.Assert(osutil.FileExists(snap.BaseDir(minInfo.SnapName())), Equals, true)

	// /snap/hello is removed when there are no more instances
	err = s.be.RemoveSnapDir(minInfo, false)
	c.Assert(err, IsNil)
	c.Assert(osutil.FileExists(snap.BaseDir(minInfo.SnapName())), Equals, false)
}

func (s *setupSuite) TestSetupComponentDoUndoSimple(c *C) {
	s.testSetupComponentDoUndo(c, "mycomp", "mysnap", "mysnap")
}

func (s *setupSuite) TestSetupComponentDoUndoInstance(c *C) {
	s.testSetupComponentDoUndo(c, "mycomp", "mysnap", "mysnap_inst")
}

func (s *setupSuite) TestSetupComponentDoIdempotent(c *C) {
	// make sure that a retry wouldn't stumble on partial work
	snapRev := snap.R(11)
	compRev := snap.R(33)

	s.testSetupComponentDo(c, "mycomp", "mysnap", "mysnap_inst", compRev, snapRev)
	s.testSetupComponentDo(c, "mycomp", "mysnap", "mysnap_inst", compRev, snapRev)
}

func (s *setupSuite) TestSetupComponentUndoIdempotent(c *C) {
	// make sure that a retry wouldn't stumble on partial work
	snapRev := snap.R(11)
	compRev := snap.R(33)

	installRecord := s.testSetupComponentDo(c, "mycomp", "mysnap", "mysnap_inst",
		compRev, snapRev)

	s.testSetupComponentUndo(c, "mycomp", "mysnap", "mysnap_inst",
		compRev, snapRev, installRecord)
	s.testSetupComponentUndo(c, "mycomp", "mysnap", "mysnap_inst",
		compRev, snapRev, installRecord)
}

func (s *setupSuite) testSetupComponentDo(c *C, compName, snapName, instanceName string, compRev, snapRev snap.Revision) *backend.InstallRecord {
	componentYaml := fmt.Sprintf(`component: %s+%s
type: test
version: 1.0
`, snapName, compName)

	compPath := snaptest.MakeTestComponent(c, componentYaml)
	cpi := snap.MinimalComponentContainerPlaceInfo(compName, compRev, instanceName, snapRev)

	installRecord, err := s.be.SetupComponent(compPath, cpi, mockDev, progress.Null)
	c.Assert(err, IsNil)
	c.Assert(installRecord, NotNil)

	// after setup the component file is in the right dir
	compFileName := instanceName + "+" + compName + "_" + compRev.String() + ".comp"
	c.Assert(osutil.FileExists(filepath.Join(dirs.SnapBlobDir, compFileName)),
		Equals, true)

	// ensure the right unit is created
	where := filepath.Join(dirs.StripRootDir(dirs.SnapMountDir),
		instanceName+"/components/"+snapRev.String()+"/"+compName)
	mup := systemd.MountUnitPath(where)
	c.Assert(mup, testutil.FileMatches, fmt.Sprintf("(?ms).*^Where=%s", where))
	compBlobPath := "/var/lib/snapd/snaps/" + compFileName
	c.Assert(mup, testutil.FileMatches, "(?ms).*^What="+regexp.QuoteMeta(compBlobPath))

	// mount dir was created
	c.Assert(osutil.FileExists(cpi.MountDir()), Equals, true)

	return installRecord
}

func (s *setupSuite) testSetupComponentUndo(c *C, compName, snapName, instanceName string, compRev, snapRev snap.Revision, installRecord *backend.InstallRecord) {
	// undo undoes the mount unit and the instdir creation
	cpi := snap.MinimalComponentContainerPlaceInfo(compName, compRev, instanceName, snapRev)

	err := s.be.UndoSetupComponent(cpi, installRecord, mockDev, progress.Null)
	c.Assert(err, IsNil)
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(cpi.MountDir()), Equals, false)
	c.Assert(osutil.FileExists(cpi.MountFile()), Equals, false)
}

func (s *setupSuite) testSetupComponentDoUndo(c *C, compName, snapName, instanceName string) {
	snapRev := snap.R(11)
	compRev := snap.R(33)

	installRecord := s.testSetupComponentDo(c, compName, snapName, instanceName,
		compRev, snapRev)

	s.testSetupComponentUndo(c, compName, snapName, instanceName,
		compRev, snapRev, installRecord)
}

func (s *setupSuite) TestSetupComponentCleanupAfterFail(c *C) {
	snapName := "mysnap"
	compName := "mycomp"
	snapRev := snap.R(11)
	compRev := snap.R(33)

	componentYaml := fmt.Sprintf(`component: %s+%s
type: test
version: 1.0
`, snapName, compName)

	compPath := snaptest.MakeTestComponent(c, componentYaml)

	cpi := snap.MinimalComponentContainerPlaceInfo(compName, compRev, snapName, snapRev)

	r := systemd.MockSystemctl(func(cmd ...string) ([]byte, error) {
		// mount unit start fails
		if len(cmd) >= 2 && cmd[0] == "reload-or-restart" &&
			strings.HasSuffix(cmd[1], ".mount") {
			return nil, fmt.Errorf("failed")
		}
		return []byte("ActiveState=inactive\n"), nil
	})
	defer r()

	installRecord, err := s.be.SetupComponent(compPath, cpi, mockDev, progress.Null)
	c.Assert(err, ErrorMatches, "failed")
	c.Check(installRecord, IsNil)

	// everything is gone
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Check(l, HasLen, 0)
	c.Assert(osutil.FileExists(cpi.MountDir()), Equals, false)
	c.Assert(osutil.FileExists(cpi.MountFile()), Equals, false)
}

func (s *setupSuite) TestSetupComponentFilesDir(c *C) {
	snapRev := snap.R(11)
	compRev := snap.R(33)
	compName := "mycomp"
	snapInstance := "mysnap_inst"
	cpi := snap.MinimalComponentContainerPlaceInfo(compName, compRev, snapInstance, snapRev)

	installRecord := s.testSetupComponentDo(c, compName, "mysnap", snapInstance, compRev, snapRev)

	err := s.be.RemoveComponentFiles(cpi, installRecord, mockDev, progress.Null)
	c.Assert(err, IsNil)
	l, _ := filepath.Glob(filepath.Join(dirs.SnapServicesDir, "*.mount"))
	c.Assert(l, HasLen, 0)
	c.Assert(osutil.FileExists(cpi.MountDir()), Equals, false)
	c.Assert(osutil.FileExists(cpi.MountFile()), Equals, false)

	err = s.be.RemoveComponentDir(cpi)
	c.Assert(err, IsNil)
	// Directory for the snap revision should be gone
	c.Assert(osutil.FileExists(filepath.Dir(cpi.MountDir())), Equals, false)
}

func (s *setupSuite) TestSetupAndRemoveKernelSnapSetup(c *C) {
	bloader := bootloadertest.Mock("mock", c.MkDir())
	bootloader.Force(bloader)

	// we don't get real mounting
	os.Setenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS", "1")
	defer os.Unsetenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS")

	// Files from the early-mounted snap
	snapdir := filepath.Join(dirs.GlobalRootDir, "run/mnt/kernel-snaps/kernel/33")
	fwdir := filepath.Join(snapdir, "firmware")
	c.Assert(os.MkdirAll(fwdir, 0755), IsNil)
	c.Assert(os.WriteFile(filepath.Join(fwdir, "bar.bin"), []byte{}, 0644), IsNil)

	// Run set-up
	err := s.be.SetupKernelSnap("kernel", snap.R(33), progress.Null)
	c.Assert(err, IsNil)

	// ensure the right unit is created
	what := filepath.Join(dirs.GlobalRootDir, "var/lib/snapd/snaps/kernel_33.snap")
	where := "/run/mnt/kernel-snaps/kernel/33"
	mup := systemd.MountUnitPath(where)
	c.Assert(mup, testutil.FileMatches, fmt.Sprintf("(?ms).*^Where=%s", where))
	c.Assert(mup, testutil.FileMatches, fmt.Sprintf("(?ms).*^What=%s", what))

	// And the kernel files
	treedir := filepath.Join(dirs.SnapdStateDir(dirs.GlobalRootDir), "kernel/kernel/33")
	c.Assert(osutil.FileExists(filepath.Join(treedir, "lib/firmware/bar.bin")), Equals, true)

	// Now test cleaning-up
	s.be.RemoveKernelSnapSetup("kernel", snap.R(33), progress.Null)
	c.Assert(osutil.FileExists(mup), Equals, false)
	c.Assert(osutil.FileExists(filepath.Join(treedir, "lib/firmware/bar.bin")), Equals, false)
}

func (s *setupSuite) TestSetupKernelSnapFailed(c *C) {
	bloader := bootloadertest.Mock("mock", c.MkDir())
	bootloader.Force(bloader)

	// we don't get real mounting
	os.Setenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS", "1")
	defer os.Unsetenv("SNAPPY_SQUASHFS_UNPACK_FOR_TESTS")

	// File from the early-mounted snap
	snapdir := filepath.Join(dirs.GlobalRootDir, "run/mnt/kernel-snaps/kernel/33")
	fwdir := filepath.Join(snapdir, "firmware")
	c.Assert(os.MkdirAll(fwdir, 0755), IsNil)
	// Force failure via unexpected file type
	c.Assert(syscall.Mkfifo(filepath.Join(fwdir, "fifo"), 0666), IsNil)

	err := s.be.SetupKernelSnap("kernel", snap.R(33), progress.Null)
	c.Assert(err, ErrorMatches, `"fifo" has unexpected file type: p---------`)

	// All has been cleaned-up
	mup := systemd.MountUnitPath("/run/mnt/kernel-snaps/kernel/33")
	treedir := filepath.Join(dirs.SnapdStateDir(dirs.GlobalRootDir), "kernel/kernel/33")
	c.Assert(osutil.FileExists(mup), Equals, false)
	c.Assert(osutil.FileExists(treedir), Equals, false)
}
