# Distributed Memory Trie (DMT)

Welcome to the Distributed Memory Trie (DMT) project! This project aims to create a highly efficient, distributed, and fault-tolerant system for managing and querying large-scale data stored in an S3-compatible storage system. The core of the project revolves around a radix tree and prefix permutation to efficiently distribute and retrieve data.

## Table of Contents

- [Introduction](#introduction)
- [Features](#features)
- [Getting Started](#getting-started)
- [Usage](#usage)
- [Project Structure](#project-structure)
- [Tests and Benchmarks](#tests-and-benchmarks)
- [Contributing](#contributing)
- [License](#license)

## Introduction

The Distributed Memory Trie (DMT) project provides a scalable and distributed system for managing prefixes in an S3-compatible storage. It leverages a radix tree for efficient storage and retrieval and a prefix permutation mechanism for load balancing and fault tolerance.

### Concepts

- **Radix Tree**: A compressed trie data structure used to store a set of strings. It's optimized for efficient lookups and minimizes memory usage by sharing common prefixes.
- **Prefix Permutation**: A mechanism to generate all possible ordered combinations of a prefix's segments. This helps in distributing load and ensuring fault tolerance by enabling multiple nodes to manage different permutations of the same prefix.

## Features

- **Efficient Storage and Retrieval**: Uses a radix tree for fast and memory-efficient storage and retrieval of data.
- **Scalability**: Designed to scale horizontally by distributing load across multiple nodes.
- **Fault Tolerance**: Ensures data is replicated across nodes and can recover from node failures.
- **Prefix Permutation**: Generates permutations of prefixes to distribute load evenly.

## Getting Started

### Prerequisites

- Go (1.16 or higher)
- An S3-compatible storage system (e.g., Amazon S3, MinIO)
- Docker and Kubernetes (for deploying the distributed system)

### Installation

Clone the repository:

```sh
git clone https://github.com/yourusername/dmt.git
cd dmt
```

Build the project:

```sh
go build -o dmt main.go
```

Run the project:

```sh
./dmt
```

## Usage

### Inserting Data

To inset data into the tree:

```go
tree := NewTree()
tree.Insert("key1", []byte("value1"))
tree.Insert("key2", []byte("value2"))
```

### Retrieving Data

To retrieve data from the tree:

```go
value, found := tree.Get("key1")
if found {
    fmt.Println(value)
}
```

### Deleting Data

To delete data from the tree:

```go
tree.Delete("key1")
```

## Project Structure

```
.
├── README.md
├── tree.go
├── tree_test.go
├── tree_benchmark_test.go
├── prefix_permutator.go
├── prefix_permutator_test.go
├── prefix_permutator_benchmark_test.go
└── ...
```
