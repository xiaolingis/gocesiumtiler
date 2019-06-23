// Copyright 2019 Massimo Federico Bonfigli

// This file contains definitions of helper functions to tailor the lidario library
// to the needs of the cesium tiler library

package lidario

import (
	"encoding/binary"
	"github.com/mfbonfigli/gocesiumtiler/converters"
	"github.com/mfbonfigli/gocesiumtiler/structs"
	"github.com/mfbonfigli/gocesiumtiler/structs/octree"
	"io"
	"log"
	"os"
	"runtime"
	"sync"
)

// NewLasFile creates a new LasFile structure which stores the points data directly into OctElement instances
// which can be retrieved by index using the GetOctElement function
func NewLasFileForTiler(fileName string, zCorrection func(lat, lon, z float64) float64, inSrid int, loader octree.Loader) (*LasFile, error) {
	// initialize the VLR array
	vlrs := []VLR{}
	las := LasFile{fileName: fileName, fileMode: "r", Header: LasHeader{}, VlrData: vlrs}
	if err := las.readForOctree(zCorrection, inSrid, loader); err != nil {
		return &las, err
	}
	return &las, nil
}

// Reads the las file and produces a LasFile struct instance loading points data into its inner list of OctElements
func (las *LasFile) readForOctree(zCorrection func(lat, lon, z float64) float64, inSrid int, loader octree.Loader) error {
	var err error
	if las.f, err = os.Open(las.fileName); err != nil {
		return err
	}
	if err = las.readHeader(); err != nil {
		return err
	}
	if err := las.readVLRs(); err != nil {
		return err
	}
	if las.fileMode != "rh" {
		recLengths := [4][4]int{{20, 18, 19, 17}, {28, 26, 27, 25}, {26, 24, 25, 23}, {34, 32, 33, 31}}

		if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][0] {
			las.usePointIntensity = true
			las.usePointUserdata = true
		} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][1] {
			las.usePointIntensity = false
			las.usePointUserdata = true
		} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][2] {
			las.usePointIntensity = true
			las.usePointUserdata = false
		} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][3] {
			las.usePointIntensity = false
			las.usePointUserdata = false
		}

		if err := las.readPointsOctElem(zCorrection, inSrid, loader); err != nil {
			return err
		}
	}
	return nil
}

// Reads all the points of the given las file and parses them into an OctElement data structure which is then stored
// in the given LasFile instance
func (las *LasFile) readPointsOctElem(zCorrection func(lat, lon, z float64) float64, inSrid int, loader octree.Loader) error {
	las.Lock()
	defer las.Unlock()
	// las.pointDataOctElement = make([]octree.OctElement, las.Header.NumberPoints)
	if las.Header.PointFormatID == 1 || las.Header.PointFormatID == 3 {
		// las.gpsData = make([]float64, las.Header.NumberPoints)
	}
	if las.Header.PointFormatID == 2 || las.Header.PointFormatID == 3 {
		// las.rgbData = make([]RgbData, las.Header.NumberPoints)
	}

	// Estimate how many bytes are used to store the points
	pointsLength := las.Header.NumberPoints * las.Header.PointRecordLength
	b := make([]byte, pointsLength)
	if _, err := las.f.ReadAt(b, int64(las.Header.OffsetToPoints)); err != nil && err != io.EOF {
		// return err
	}

	// Intensity and userdata are both optional. Figure out if they need to be read.
	// The only way to do this is to compare the point record length by point format
	recLengths := [4][4]int{{20, 18, 19, 17}, {28, 26, 27, 25}, {26, 24, 25, 23}, {34, 32, 33, 31}}

	if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][0] {
		las.usePointIntensity = true
		las.usePointUserdata = true
	} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][1] {
		las.usePointIntensity = false
		las.usePointUserdata = true
	} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][2] {
		las.usePointIntensity = true
		las.usePointUserdata = false
	} else if las.Header.PointRecordLength == recLengths[las.Header.PointFormatID][3] {
		las.usePointIntensity = false
		las.usePointUserdata = false
	}

	numCPUs := runtime.NumCPU()
	var wg sync.WaitGroup
	blockSize := las.Header.NumberPoints / numCPUs
	var startingPoint int
	for startingPoint < las.Header.NumberPoints {
		endingPoint := startingPoint + blockSize
		if endingPoint >= las.Header.NumberPoints {
			endingPoint = las.Header.NumberPoints - 1
		}
		wg.Add(1)
		go func(pointSt, pointEnd int) {
			defer wg.Done()

			var offset int
			// var p PointRecord0
			for i := pointSt; i <= pointEnd; i++ {
				offset = i * las.Header.PointRecordLength
				// p := PointRecord0{}
				X := float64(int32(binary.LittleEndian.Uint32(b[offset:offset+4])))*las.Header.XScaleFactor + las.Header.XOffset
				offset += 4
				Y := float64(int32(binary.LittleEndian.Uint32(b[offset:offset+4])))*las.Header.YScaleFactor + las.Header.YOffset
				offset += 4
				Z := float64(int32(binary.LittleEndian.Uint32(b[offset:offset+4])))*las.Header.ZScaleFactor + las.Header.ZOffset
				offset += 4

				var R, G, B, Intensity, Classification uint8
				if las.usePointIntensity {
					Intensity = uint8(binary.LittleEndian.Uint16(b[offset:offset+2]) / 256)
					offset += 2
				}
				//p.BitField = PointBitField{Value: b[offset]}
				offset++
				//p.ClassBitField = ClassificationBitField{Value: b[offset]}
				Classification = b[offset]
				offset++
				// p.ScanAngle = int8(b[offset])
				offset++
				if las.usePointUserdata {
					// p.UserData = b[offset]
					offset++
				}
				// p.PointSourceID = binary.LittleEndian.Uint16(b[offset : offset+2])
				offset += 2

				// las.pointData[i] = p

				if las.Header.PointFormatID == 1 || las.Header.PointFormatID == 3 {
					// las.gpsData[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[offset : offset+8]))
					offset += 8
				}
				if las.Header.PointFormatID == 2 || las.Header.PointFormatID == 3 {
					//rgb := RgbData{}
					R = uint8(binary.LittleEndian.Uint16(b[offset:offset+2]) / 256)
					offset += 2
					G = uint8(binary.LittleEndian.Uint16(b[offset:offset+2]) / 256)
					offset += 2
					B = uint8(binary.LittleEndian.Uint16(b[offset:offset+2]) / 256)
					offset += 2
					// las.rgbData[i] = rgb
				}
				tr, err := converters.Convert(inSrid, 4326, structs.Coordinate{X: &X, Y: &Y, Z: &Z})
				if err != nil {
					log.Fatal(err)
				}
				elem := *octree.NewOctElement(*tr.X, *tr.Y, zCorrection(*tr.X, *tr.Y, *tr.Z), R, G, B, Intensity, Classification)
				loader.AddElement(&elem)
				// las.pointDataOctElement[i] = elem
			}
		}(startingPoint, endingPoint)
		startingPoint = endingPoint + 1
	}
	wg.Wait()
	converters.DeallocateProjection(inSrid)
	return nil
}
