package io

import (
	"encoding/json"
	"errors"
	"fmt"
	"go_cesium_tiler/converters"
	"go_cesium_tiler/structs"
	"go_cesium_tiler/structs/octree"
	"go_cesium_tiler/utils"
	"io/ioutil"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Continually consumes WorkUnits submitted to a work channel producing corresponding content.pnts files and tileset.json files
// continues working until work channel is closed or if an error is raised. In this last case submits the error to an error
// channel before quitting
func Consume(workchan chan *WorkUnit, errchan chan error, wg *sync.WaitGroup) {
	for {
		// get work from channel
		work, ok := <-workchan
		if !ok {
			// channel was closed by producer, quit infinite loop
			break
		}

		// do work
		err := doWork(work)

		// if there were errors during work send in error channel and quit
		if err != nil {
			errchan <- err
			fmt.Println("exception in consumer worker")
			break
		}
	}

	// signal waitgroup finished work
	wg.Done()
}

// Takes a workunit and writes the corresponding content.pnts and tileset.json files
func doWork(workUnit *WorkUnit) error {
	// writes the content.pnts file
	err := writeBinaryPnts(*workUnit)
	if err != nil {
		return err
	}
	if !workUnit.OctNode.IsLeaf {
		// if the node has children also writes the tileset.json file
		err := writeJsonTileset(*workUnit)
		if err != nil {
			return err
		}
	}
	return nil
}

// Writes a content.pnts binary files from the given WorkUnit
func writeBinaryPnts(workUnit WorkUnit) error {
	parentFolder := workUnit.BasePath
	node := workUnit.OctNode

	// Create base folder if it does not exist
	if _, err := os.Stat(parentFolder); os.IsNotExist(err) {
		err := os.MkdirAll(parentFolder, 0777)
		if err != nil {
			return err
		}
	}

	// Constructing pnts output file path
	pntsFilePath := path.Join(parentFolder, "content.pnts")

	pointNo := len(node.Items)
	coords := make([]float64, pointNo*3)
	colors := make([]uint8, pointNo*3)
	intensities := make([]uint8, pointNo)
	classifications := make([]uint8, pointNo)

	// Decomposing tile point properties in separate sublists for coords, colors, intensities and classifications
	for i := 0; i < len(node.Items); i++ {
		element := node.Items[i]
		srcCoord := structs.Coordinate{
			X: &element.X,
			Y: &element.Y,
			Z: &element.Z,
		}

		// Convert coords according to cesium CRS
		outCrd, err := converters.ConvertToWGS84Cartesian(srcCoord, workUnit.Opts.Srid)
		if err != nil {
			return err
		}

		coords[i*3] = *outCrd.X
		coords[i*3+1] = *outCrd.Y
		coords[i*3+2] = *outCrd.Z

		colors[i*3] = element.R
		colors[i*3+1] = element.G
		colors[i*3+2] = element.B

		intensities[i] = element.Intensity
		classifications[i] = element.Classification

	}

	// Evaluating average X, Y, Z to express coords relative to tile center
	var avgX, avgY, avgZ float64
	for i := 0; i < pointNo; i++ {
		avgX = avgX + coords[i*3]
		avgY = avgY + coords[i*3+1]
		avgZ = avgZ + coords[i*3+2]
	}
	avgX /= float64(pointNo)
	avgY /= float64(pointNo)
	avgZ /= float64(pointNo)

	// Normalizing coordinates relative to average
	for i := 0; i < pointNo; i++ {
		coords[i*3] -= avgX
		coords[i*3+1] -= avgY
		coords[i*3+2] -= avgZ
	}
	positionBytes := utils.ConvertTruncateFloat64ToFloat32ByteArray(coords)

	// Feature table
	featureTableStr := generateFeatureTableJson(avgX, avgY, avgZ, pointNo, 0)
	featureTableLen := len(featureTableStr)
	featureTableBytes := []byte(featureTableStr)

	// Batch table
	batchTableStr := generateBatchTableJson(pointNo, 0)
	batchTableLen := len(batchTableStr)
	batchTableBytes := []byte(batchTableStr)

	// Appending binary content to slice
	outputByte := make([]byte, 0)
	outputByte = append(outputByte, []byte("pnts")...)                 // magic
	outputByte = append(outputByte, utils.ConvertIntToByteArray(1)...) // version number
	byteLength := 28 + featureTableLen + len(positionBytes) + len(colors)
	outputByte = append(outputByte, utils.ConvertIntToByteArray(byteLength)...)
	outputByte = append(outputByte, utils.ConvertIntToByteArray(featureTableLen)...)                       // feature table length
	outputByte = append(outputByte, utils.ConvertIntToByteArray(len(positionBytes)+len(colors))...)        // feature table binary length
	outputByte = append(outputByte, utils.ConvertIntToByteArray(batchTableLen)...)                         // batch table length
	outputByte = append(outputByte, utils.ConvertIntToByteArray(len(intensities)+len(classifications))...) // batch table binary length
	outputByte = append(outputByte, featureTableBytes...)                                                  // feature table
	outputByte = append(outputByte, positionBytes...)                                                      // positions array
	outputByte = append(outputByte, colors...)                                                             // colors array
	outputByte = append(outputByte, batchTableBytes...)                                                    // batch table
	outputByte = append(outputByte, intensities...)                                                        // intensities array
	outputByte = append(outputByte, classifications...)                                                    // classifications array

	// Write binary content to file
	err := ioutil.WriteFile(pntsFilePath, outputByte, 0777)

	if err != nil {
		return err
	}
	return nil
}

// Generates the json representation of the feature table
func generateFeatureTableJson(x, y, z float64, pointNo int, spaceNo int) string {
	sb := ""
	sb += "{\"POINTS_LENGTH\":" + strconv.Itoa(pointNo) + ","
	sb += "\"RTC_CENTER\":[" + fmt.Sprintf("%f", x) + strings.Repeat("0", spaceNo)
	sb += "," + fmt.Sprintf("%f", y) + "," + fmt.Sprintf("%f", z) + "],"
	sb += "\"POSITION\":" + "{\"byteOffset\":" + "0" + "},"
	sb += "\"RGB\":" + "{\"byteOffset\":" + strconv.Itoa(pointNo*12) + "}}"
	headerByteLength := len([]byte(sb))
	paddingSize := headerByteLength % 4
	if paddingSize != 0 {
		return generateFeatureTableJson(x, y, z, pointNo, 4-paddingSize)
	}
	return sb
}

// Generates the json representation of the batch table
func generateBatchTableJson(pointNumber, spaceNumber int) string {
	sb := ""
	sb += "{\"INTENSITY\":" + "{\"byteOffset\":" + "0" + ", \"componentType\":\"UNSIGNED_BYTE\", \"type\":\"SCALAR\"},"
	sb += "\"CLASSIFICATION\":" + "{\"byteOffset\":" + strconv.Itoa(pointNumber) + ", \"componentType\":\"UNSIGNED_BYTE\", \"type\":\"SCALAR\"}}"
	sb += strings.Repeat(" ", spaceNumber)
	headerByteLength := len([]byte(sb))
	paddingSize := headerByteLength % 4
	if paddingSize != 0 {
		return generateBatchTableJson(pointNumber, 4-paddingSize)
	}
	return sb
}

// Writes the tileset.json file for the given WorkUnit
func writeJsonTileset(workUnit WorkUnit) error {
	parentFolder := workUnit.BasePath
	node := workUnit.OctNode

	// Create base folder if it does not exist
	if _, err := os.Stat(parentFolder); os.IsNotExist(err) {
		err := os.MkdirAll(parentFolder, 0777)
		if err != nil {
			return err
		}
	}

	// tileset.json file
	file := path.Join(parentFolder, "tileset.json")
	jsonData, err := createTilesetJson(node, workUnit.Opts)
	if err != nil {
		return err
	}

	// Writes the tileset.json binary content to the given file
	err = ioutil.WriteFile(file, jsonData, 0666)
	if err != nil {
		return err
	}

	return nil
}

// Generates the tileset.json content for the given octnode and tileroptions
func createTilesetJson(node *octree.OctNode, opts *octree.TilerOptions) ([]byte, error) {
	if !node.IsLeaf {
		tileset := Tileset{}
		tileset.Asset = Asset{Version: "0.0"}
		tileset.GeometricError = computeGeometricError(node)
		root := Root{}
		for i, child := range node.Children {
			if child != nil && child.GlobalChildrenCount > 0 {
				childJson := Child{}
				filename := "tileset.json"
				if child.IsLeaf {
					filename = "content.pnts"
				}
				childJson.Content = Content{
					Url: strconv.Itoa(i) + "/" + filename,
				}
				reg, err := converters.Convert2DBoundingboxToWGS84Region(child.BoundingBox, opts.Srid)
				if err != nil {
					return nil, err
				}
				childJson.BoundingVolume = BoundingVolume{
					Region: reg,
				}
				childJson.GeometricError = computeGeometricError(child)
				childJson.Refine = "add"
				root.Children = append(root.Children, childJson)
			}
		}
		root.Content = Content{
			Url: "content.pnts",
		}
		reg, err := converters.Convert2DBoundingboxToWGS84Region(node.BoundingBox, opts.Srid)
		if err != nil {
			return nil, err
		}
		root.BoundingVolume = BoundingVolume{
			Region: reg,
		}
		root.GeometricError = computeGeometricError(node)
		root.Refine = "add"
		tileset.Root = root

		// Outputting a formatted json file
		e, err := json.MarshalIndent(tileset, "", "\t")
		if err != nil {
			return nil, err
		}

		return e, nil
	}

	return nil, errors.New("this node is a leaf, cannot create tileset json for it")
}

// Computes the geometric error for the given OctNode
func computeGeometricError(node *octree.OctNode) float64 {
	volume := node.BoundingBox.GetVolume()
	totalRenderedPoints := int64(node.LocalChildrenCount)
	parent := node.Parent
	for parent != nil {
		for _, e := range parent.Items {
			if node.BoundingBox.CanContain(e) {
				totalRenderedPoints++
			}
		}
		parent = parent.Parent
	}
	densityWithAllPoints := math.Pow(volume/float64(totalRenderedPoints+node.GlobalChildrenCount-int64(node.LocalChildrenCount)), 0.333)
	densityWIthOnlyThisTile := math.Pow(volume/float64(totalRenderedPoints), 0.333)
	return densityWIthOnlyThisTile - densityWithAllPoints

}