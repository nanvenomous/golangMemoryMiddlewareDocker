#!/bin/bash

echo "Memory Stress Test Script"
echo "========================="

SERVER_URL="http://localhost:8080"

# Function to check if server is running
check_server() {
    if ! curl -s "$SERVER_URL" > /dev/null 2>&1; then
        echo "Error: Server is not running at $SERVER_URL"
        echo "Please start the server first with: docker-compose up"
        exit 1
    fi
}

# Function to show memory status
show_status() {
    echo "Current memory status:"
    curl -s "$SERVER_URL/status"
    echo ""
}

# Function to allocate memory
allocate_memory() {
    local mb=$1
    echo "Allocating ${mb}MB of memory..."
    response=$(curl -s -X POST "$SERVER_URL/allocate?mb=$mb")
    echo "$response"
}

# Main stress test
stress_test() {
    echo "Starting memory stress test..."
    echo "Container memory limit: 128MB"
    echo ""
    
    check_server
    show_status
    
    # Gradually increase memory usage
    for i in {1..20}; do
        echo "=== Iteration $i ==="
        allocate_memory 40
        
        # Check if container is still responding
        if ! curl -s "$SERVER_URL" > /dev/null 2>&1; then
            echo "Container appears to have crashed!"
            break
        fi
        
        sleep .5
    done
    
    echo "Stress test completed"
}

# Command line interface
case "${1:-stress}" in
    "stress")
        stress_test
        ;;
    "status")
        check_server
        show_status
        ;;
    "allocate")
        if [ -z "$2" ]; then
            echo "Usage: $0 allocate <MB>"
            exit 1
        fi
        check_server
        allocate_memory "$2"
        ;;
    "help")
        echo "Usage: $0 [command] [args]"
        echo ""
        echo "Commands:"
        echo "  stress          Run automated stress test (default)"
        echo "  status          Show current memory status"
        echo "  allocate <MB>   Allocate specific amount of memory"
        echo "  help            Show this help"
        echo ""
        echo "Examples:"
        echo "  $0                    # Run stress test"
        echo "  $0 status             # Check memory status"
        echo "  $0 allocate 50        # Allocate 50MB"
        ;;
    *)
        echo "Unknown command: $1"
        echo "Use '$0 help' for usage information"
        exit 1
        ;;
esac
