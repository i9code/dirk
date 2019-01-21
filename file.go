package dirk

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Dirent struct {
	name string
	path string
	mode os.FileMode
}

func (de Dirent) IsDir() bool     { return de.mode&os.ModeDir != 0 }
func (de Dirent) IsRegular() bool { return de.mode&os.ModeType == 0 }
func (de Dirent) IsSymlink() bool { return de.mode&os.ModeSymlink != 0 }
func (de Dirent) IsHidden() bool  { return string(de.name[0]) == "." }

type Dirents []*Dirent

func (l Dirents) Len() int           { return len(l) }
func (l Dirents) Less(i, j int) bool { return l[i].name < l[j].name }
func (l Dirents) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }

type File struct {
	*Dirent
	File os.FileInfo
	Path string
	Name string

	Number   int
	Active   bool
	Selected bool
	Ignore   bool
	Nick     string

	NumLines int
	MapLine  map[int]string
}

func MakeFile(dir string) (file File, err error) {
	f, err := os.Stat(dir)
	if err != nil {
		return
	}
	dirent := &Dirent{
		name: filepath.Base(dir),
		path: dir,
		mode: f.Mode(),
	}
	file = File{
		File: f,
		Name: dirent.name,
		Nick: dirent.name,
		Path: dirent.path,
	}
	file.MapLine = make(map[int]string)
	return
}

func (f File) IsDir() bool     { return f.File.Mode()&os.ModeDir != 0 }
func (f File) IsRegular() bool { return f.File.Mode()&os.ModeType == 0 }
func (f File) IsSymlink() bool { return f.File.Mode()&os.ModeSymlink != 0 }
func (f File) IsHidden() bool  { return string(f.Name[0]) == "." }

func (f File) GetExte() string { return getExte(f) }
func (f File) GetIcon() string { return getIcon(f) }
func (f File) GetMime() string { return getMime(f) }

func (f File) SizeINT(du bool) int64  { return getSize(f, du) }
func (f File) SizeSTR(du bool) string { return byteCountSI(f.SizeINT(du)) }

func (f File) Parent() string          { return getParent(f) }
func (f File) ParentPath() string      { return getParentPath(f) }
func (f File) SiblingPaths() []string  { return elements(f.ParentPath()) }
func (f File) Siblings() []string      { return basename(f.SiblingPaths()) }
func (f File) SiblingNr() int          { return len(f.Siblings()) }
func (f File) AncestorPaths() []string { return ancestor(f.ParentPath()) }
func (f File) Ancestors() []string     { return basename(f.AncestorPaths()) }
func (f File) AncestorNr() int         { return len(f.Ancestors()) }
func (f File) ChildrenPaths() []string { return elements(f.Path) }
func (f File) Childrens() []string     { return basename(f.ChildrenPaths()) }
func (f File) ChildrenNr() int         { return len(f.Childrens()) }

type Files []*File

func MakeFiles(path ...string) (files Files, err error) {
	files = Files{}
	for i := range path {
		if file, err := MakeFile(path[i]); err != nil {
			return files, err
		} else {
			files = append(files, &file)
		}
	}
	return files, nil
}

func (e Files) String(i int) string    { return e[i].Name }
func (e Files) Len() int               { return len(e) }
func (e Files) Swap(i, j int)          { e[i], e[j] = e[j], e[i] }
func (e Files) Less(i, j int) bool     { return e[i].Nick[0:] < e[j].Nick[0:] }
func (e Files) SortSize(i, j int) bool { return e[i].SizeINT(DiskUse) < e[j].SizeINT(DiskUse) }

//func (e Files) SortDate(i, j int) bool { return e[i].BrtTime.Before(e[j].BrtTime) }

type Element struct {
	sync.RWMutex
	files []*File
}

func (e *Element) Add(item File) {
	e.Lock()
	defer e.Unlock()
	e.files = append(e.files, &item)
}

func fileList(recurrent bool, dir File) (paths Files, err error) {
	var wg sync.WaitGroup
	tempfiles := Element{}
	var file File
	if recurrent {
		err = Walk(dir.Path, &Options{
			Callback: func(osPathname string, de *Dirent) (err error) {
				wg.Add(1)
				go func() {
					if file, err = MakeFile(osPathname); err == nil {
						tempfiles.Add(file)
					}
					wg.Done()
				}()
				return nil
			},
			Unsorted:      true,
			NoHidden:      !IncHidden,
			Ignore:        IgnoreRecur,
			ScratchBuffer: make([]byte, 64*1024),
		})
	} else {
		children, err := ReadDirnames(dir.Path, nil)
		if err != nil {
			return paths, err
		}
		sort.Strings(children)
		for _, child := range children {
			osPathname := path.Join(dir.Path + "/" + child)
			wg.Add(1)
			go func() {
				if file, err = MakeFile(osPathname); err == nil {
					tempfiles.Add(file)
				}
				wg.Done()
			}()
		}
	}
	wg.Wait()
	return tempfiles.files, nil
}

func chooseFile(incFolder, incFiles, incHidden, recurrent bool, dir File) (list Files) {
	files, folder := Files{}, Files{}

	paths, _ := fileList(recurrent, dir)
	for _, f := range paths {
		for i := range IgnoreSlice {
			if f.Name == IgnoreSlice[i] {
				goto Exit
			}
		}
		if f.IsDir() {
			if !f.IsHidden() || incHidden {
				folder = append(folder, f)
			}
		} else {
			if !f.IsHidden() || incHidden {
				if Recurrent {
					f.Nick = f.Path
				}
				files = append(files, f)
			}
		}
	Exit:
	}
	if incFolder && !Recurrent {
		sort.Sort(folder)
		list = append(list, folder...)
	}
	if incFiles {
		sort.Sort(files)
		list = append(list, files...)
	}
	for i := range list {
		list[i].Number = i
	}
	return
}

func byteCountSI(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
}

func byteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(b)/float64(div), "KMGTPE"[exp])
}

func getSize(file File, dumode bool) (size int64) {
	dir := file.Path
	if dumode {
		Walk(dir, &Options{
			Callback: func(osPathname string, de *Dirent) (err error) {
				f, err := os.Stat(osPathname)
				if err != nil {
					return
				}
				size += f.Size()
				return nil
			},
			Unsorted:      true,
			ScratchBuffer: make([]byte, 64*1024),
		})
	} else {
		size = file.File.Size()
	}
	return
}

func elements(dir string) (childs []string) {
	childs = []string{}
	if someChildren, err := ReadDirnames(dir, nil); err == nil {
		for i := range someChildren {
			childs = append(childs, dir+someChildren[i])
		}
	}
	return
}

func ancestor(dir string) (ances []string) {
	ances = append(ances, "/")
	joiner := ""
	for _, el := range strings.Split(dir, "/") {
		if el == "" {
			continue
		}
		joiner += "/" + el
		ances = append(ances, joiner)
	}
	return
}

func basename(paths []string) (names []string) {
	for i := range paths {
		names = append(names, filepath.Base(paths[i]))
	}
	return
}

func parentInfo(dir string) (parent, parentPath string) {
	parent, parentPath = "/", "/"
	if dir != "/" {
		dir = path.Clean(dir)
		parentPath, _ = path.Split(dir)
		parent = strings.TrimRight(parentPath, "/")
		_, parent = path.Split(parent)
		if parent == "" {
			parent, parentPath = "/", "/"
		}
	}
	return
}

func getParent(f File) string {
	parent, _ := parentInfo(f.Path)
	return parent
}

func getParentPath(f File) string {
	_, parentPath := parentInfo(f.Path)
	return parentPath
}

func getIcon(f File) string {
	if f.IsDir() {
		return categoryicons["folder/folder"]
	} else {
		icon := fileicons[getExte(f)]
		if icon == "" {
			return categoryicons["file/default"]
		}
		return icon
	}
}

func getMime(f File) string {
	if f.IsDir() {
		return "folder/folder"
	} else {
		mime, _, _ := DetectFile(f.Path)
		if mime == "" {
			return "file/default"
		}
		return mime
	}
}

func getExte(f File) string {
	if f.IsDir() {
		return "."
	} else {
		extension := path.Ext(f.Path)
		return extension
	}
}

func timespecToTime(ts syscall.Timespec) time.Time {
	return time.Unix(int64(ts.Sec), int64(ts.Nsec))
}
