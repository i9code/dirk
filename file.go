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

	"github.com/pkg/errors"
)

var (
	IgnoreSlice = []string{".git"}
	IgnoreRecur = []string{"node_modules", ".git"}
	DiskUse     = false
	wg          sync.WaitGroup
	channel     = make(chan File)
)

type Dirent struct {
	name string
	path string
	file os.FileInfo
	mode os.FileMode
	stat *syscall.Stat_t
}

func (de Dirent) IsDir() bool     { return de.mode&os.ModeDir != 0 }
func (de Dirent) IsRegular() bool { return de.mode&os.ModeType == 0 }
func (de Dirent) IsSymlink() bool { return de.mode&os.ModeSymlink != 0 }
func (de Dirent) IsHidden() bool  { return string(de.name[0]) == "." }

func MakeDirent(osPathname string) (*Dirent, error) {
	f, err := os.Stat(osPathname)
	if err != nil {
		return nil, errors.Wrap(err, "cannot lstat")
	}
	fstat := f.Sys().(*syscall.Stat_t)
	return &Dirent{
		name: filepath.Base(osPathname),
		path: osPathname,
		file: f,
		mode: f.Mode(),
		stat: fstat,
	}, nil
}

type Dirents []*Dirent

func (l Dirents) Len() int           { return len(l) }
func (l Dirents) Less(i, j int) bool { return l[i].name < l[j].name }
func (l Dirents) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }

type File struct {
	*Dirent
	Mode os.FileMode
	Path string
	Name string

	Parent        string
	ParentPath    string
	Childrens     []string
	ChildrenPaths []string
	ChildrenNr    int
	Ancestors     []string
	AncestorPaths []string
	AncestorNr    int
	Siblings      []string
	SiblingPaths  []string
	SiblingNr     int

	Mime string
	Exte string
	Icon string

	IsDir  bool
	Hidden bool
	Size   int64

	BrtTime time.Time
	AccTime time.Time
	ChgTime time.Time

	Number   int
	Active   bool
	Selected bool
	Ignore   bool
	Nick     string

	NumLines int
	MapLine  map[int]string
}

func MakeFile(dir string) (file File, err error) {
	dirent, err := MakeDirent(dir)
	if err != nil {
		return
	}

	parent, parentPath := parentInfo(dir)
	file = File{
		Name:       dirent.name,
		Nick:       dirent.name,
		Path:       dirent.path,
		Parent:     parent,
		ParentPath: parentPath,
		Size:       dirent.file.Size(),
		Mode:       dirent.file.Mode(),
		IsDir:      dirent.IsDir(),
		Hidden:     dirent.IsHidden(),
		BrtTime:    timespecToTime(dirent.stat.Mtim),
		AccTime:    timespecToTime(dirent.stat.Atim),
		ChgTime:    timespecToTime(dirent.stat.Ctim),
	}

	if dirent.file.IsDir() {
		file.Size = getSize(dirent, DiskUse)
		file.Exte = "."
		file.Mime = "folder/folder"
		file.Icon = categoryicons["folder/folder"]
		file.ChildrenPaths = elements(dir)
		file.Childrens = basename(file.ChildrenPaths)
		file.ChildrenNr = len(file.Childrens)
	} else {
		extension := path.Ext(dir)
		mime, _, _ := DetectFile(dir)
		file.Exte = extension
		file.Mime = mime
		file.Icon = fileicons[extension]
		if file.Icon == "" {
			file.Icon = categoryicons["file/default"]
		}
	}
	file.SiblingPaths = elements(file.ParentPath)
	file.Siblings = basename(file.SiblingPaths)
	file.SiblingNr = len(file.Siblings)
	file.AncestorPaths = ancestor(file.ParentPath)
	file.Ancestors = basename(file.AncestorPaths)
	file.AncestorNr = len(file.Ancestors)
	file.MapLine = make(map[int]string)
	return
}

func (f File) GetSize() string { return byteCountIEC(f.Size) }

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
func (e Files) SortSize(i, j int) bool { return e[i].Size < e[j].Size }
func (e Files) SortDate(i, j int) bool { return e[i].BrtTime.Before(e[j].BrtTime) }

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
			NoHidden:      true,
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
		if f.IsDir {
			if !f.Hidden || incHidden {
				folder = append(folder, f)
			}
		} else {
			if !f.Hidden || incHidden {
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
		for _, d := range folder {
			list = append(list, d)
		}
	}
	if incFiles {
		sort.Sort(files)
		for _, f := range files {
			list = append(list, f)
		}
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

func getSize(dirent *Dirent, dumode bool) (size int64) {
	dir := dirent.path
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
		size = dirent.file.Size()
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

func timespecToTime(ts syscall.Timespec) time.Time {
	return time.Unix(int64(ts.Sec), int64(ts.Nsec))
}
