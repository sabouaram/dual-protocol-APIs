package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	pb "dual-protocol-APIs/proto"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type server struct {
	pb.UnimplementedTaskServiceServer
	tasks  []*pb.TaskResponse
	nextID int32
	mu     sync.Mutex // Mutex to protect the tasks slice
}

func UnaryServerInterceptor() grpc.UnaryServerInterceptor {

	return func(

		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,

	) (interface{}, error) {

		start := time.Now()

		fmt.Printf("Received request for %s\n", info.FullMethod)

		resp, err := handler(ctx, req)
		duration := time.Since(start)

		if err != nil {
			fmt.Printf("Request for %s failed: %v\n", info.FullMethod, err)
		} else {
			fmt.Printf("Request for %s succeeded (took %v)\n", info.FullMethod, duration)
		}

		return resp, err
	}
}

func (s *server) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.TaskResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nextID == 0 {
		s.nextID = 1
	}

	task := &pb.TaskResponse{
		Id:          s.nextID,
		Title:       req.Title,
		Description: req.Description,
	}

	s.nextID++
	s.tasks = append(s.tasks, task)

	return task, nil
}

func (s *server) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	return &pb.ListTasksResponse{Tasks: s.tasks}, nil
}

func (s *server) DeleteTask(ctx context.Context, req *pb.DeleteTaskRequest) (*pb.TaskResponse, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, task := range s.tasks {
		if task.Id == req.Id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			return task, nil
		}
	}

	return nil, errors.New("task not found")
}

func main() {
	// Create the gRPC server with logging interceptor
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryServerInterceptor()),
	)
	taskService := &server{}
	pb.RegisterTaskServiceServer(grpcServer, taskService)
	// Enable Reflection for proto auto discovery
	reflection.Register(grpcServer)

	// Create the Gin router
	router := gin.Default()

	router.POST("/tasks", func(c *gin.Context) {

		var req pb.CreateTaskRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		resp, err := taskService.CreateTask(c, &req)

		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, resp)
	})

	router.GET("/tasks", func(c *gin.Context) {

		resp, err := taskService.ListTasks(c, &pb.ListTasksRequest{})

		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, resp.Tasks)
	})

	router.DELETE("/tasks/:id", func(c *gin.Context) {

		idParam := c.Param("id")
		id, err := strconv.Atoi(idParam)

		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid task ID"})
			return
		}

		req := &pb.DeleteTaskRequest{Id: int32(id)}
		resp, err := taskService.DeleteTask(c, req)

		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, resp)
	})

	// Create a custom http.Server to handle both HTTP/1.x and HTTP/2 (h2c)
	httpServer := &http.Server{
		Addr: ":50051",
		Handler: h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Detect if it's a gRPC request or HTTP request
			if r.ProtoMajor == 2 && r.Header.Get("content-type") == "application/grpc" {
				grpcServer.ServeHTTP(w, r)
			} else {
				router.ServeHTTP(w, r)
			}
		}), &http2.Server{}),
	}

	log.Println("Serving gRPC and HTTP on :50051")

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
