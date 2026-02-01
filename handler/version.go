package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
)

func GetVersion(c *gin.Context) {
	common.Success(c, consts.Version)
}
