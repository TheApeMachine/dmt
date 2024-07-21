# Distributed Memory Trie (DMT)

Welcome to the Distributed Memory Trie (DMT) project! This project aims to create a highly efficient, distributed, and fault-tolerant system for managing and querying large-scale data stored in an S3-compatible storage system. The core of the project revolves around a radix tree and prefix permutation to efficiently distribute and retrieve data.

## Table of Contents

- [Introduction](#introduction)
- [Features](#features)
- [Getting Started](#getting-started)
- [Usage](#usage)
- [Prefix Permutations and Relational Data](#prefix-permutations-and-relational-data)
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
- **Prefix Permutation**: Generates permutations of prefixes to distribute load evenly and make data relational.

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

### Prefix Permuation

To generate permutations of a prefix:

```go
perm := NewPrefixPermutator("a/b/c")
permutations := perm.Permute()
fmt.Println(permutations)
```

## Prefix Permutations and Relational Data

### Concept

The primary purpose of prefix permutations is to establish relationships between data that would otherwise be difficult to find using a single-prefix order. By permuting the prefixes, you can query different permutations to uncover related data. This makes the data "relational", allowing you to efficiently retrieve associated information based on various prefix orders.

### Simple Example

Consider a scenario where you store user information with the following prefix structure:

```
users/{userid}/profile
users/{userid}/orders/{orderid}
users/{userid}/settings
```


Using prefix permutations, you can generate and query the following permutations to find related data:

<details>
<summary>Permutations for <code>users/{userid}/profile</code></summary>

1. `users/{userid}/profile`
2. `users/profile/{userid}`
3. `{userid}/users/profile`
4. `{userid}/profile/users`
5. `profile/users/{userid}`
6. `profile/{userid}/users`

</details>

<details>
<summary>Permutations for <code>users/{userid}/orders/{orderid}</code></summary>

1. `users/{userid}/orders/{orderid}`
2. `users/{userid}/{orderid}/orders`
3. `users/orders/{userid}/{orderid}`
4. `users/orders/{orderid}/{userid}`
5. `users/{orderid}/{userid}/orders`
6. `users/{orderid}/orders/{userid}`
7. `{userid}/users/orders/{orderid}`
8. `{userid}/users/{orderid}/orders`
9. `{userid}/orders/users/{orderid}`
10. `{userid}/orders/{orderid}/users`
11. `{userid}/{orderid}/users/orders`
12. `{userid}/{orderid}/orders/users`
13. `orders/users/{userid}/{orderid}`
14. `orders/users/{orderid}/{userid}`
15. `orders/{userid}/users/{orderid}`
16. `orders/{userid}/{orderid}/users`
17. `orders/{orderid}/users/{userid}`
18. `orders/{orderid}/{userid}/users`
19. `{orderid}/users/{userid}/orders`
20. `{orderid}/users/orders/{userid}`
21. `{orderid}/{userid}/users/orders`
22. `{orderid}/{userid}/orders/users`
23. `{orderid}/orders/users/{userid}`
24. `{orderid}/orders/{userid}/users`

</details>

<details>
<summary>Permutations for <code>users/{userid}/settings</code></summary>

1. `users/{userid}/settings`
2. `users/settings/{userid}`
3. `{userid}/users/settings`
4. `{userid}/settings/users`
5. `settings/users/{userid}`
6. `settings/{userid}/users`

</details>

### Querying Permutations

By storing and querying these permutations, the system can efficiently distribute the data across multiple nodes and ensure fault tolerance. If a particular node managing a specific permutation fails, other nodes managing different permutations of the same prefix can still serve the data.

For instance, if you need to retrieve all settings of a user, you could query:

`users/{userid}/settings`

If you want all settings, and have the user data "joined" onto the setting data, you could query:

`settings/`

If you want everything related to a user, you could query:

`users/{userid}/`

Or even simply:

`users/`

to retrieve all data related to all users.

By generating all the permutations of a prefix, it allows us to circumvent issues we have when data is nested in a single prefix.

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
