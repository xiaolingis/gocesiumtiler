package converters

import (
	"github.com/mfbonfigli/gocesiumtiler/structs/geometry"
)

type CoordinateConverter interface {
	ConvertCoordinateSrid(sourceSrid int, targetSrid int, coord geometry.Coordinate) (geometry.Coordinate, error)
	Convert2DBoundingboxToWGS84Region(bbox *geometry.BoundingBox, srid int) ([]float64, error)
	ConvertToWGS84Cartesian(coord geometry.Coordinate, sourceSrid int) (geometry.Coordinate, error)
	Cleanup()
}
