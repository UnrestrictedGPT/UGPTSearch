# UGPTSearch

A robust Go based local search API that rotates requests across multiple Searx instances for improved reliability and load distribution.

## Features

- **Instance Rotation**: Automatically distributes and rotates search requests across multiple Searx instances
- **Fault Tolerance**: Seamlessly handles instance failures by rotating to available alternatives
- **Load Balancing**: Prevents overloading individual Searx instances
- **Simple API**: Easy-to-use interface for performing searches

## Installation
