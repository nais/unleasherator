# How to contribute

We welcome contributions to this project in the form of issues and pull requests.

## Issues

Issues are a quick way to point out a bug. If you find a bug or documentation error in Unleash, this is the place to start.

## Pull Requests

Pull requests are the best way to propose changes to the codebase. We actively welcome your pull requests:

1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. Ensure the test are passing by running `make all test`.
4. Create that pull request!

## Testing

All code should be tested. Controller code should be tested with [envtest](https://github.com/kubernetes-sigs/controller-runtime/blob/master/pkg/envtest/README.md) and [Ginkgo](https://onsi.github.io/ginkgo/). All other code is tested with [testify](https://github.com/stretchr/testify).

You can read more about writing Ginko tests [here](https://onsi.github.io/ginkgo/) as well as writing kubebuilder envtests [here](https://book.kubebuilder.io/reference/envtest.html).

## Style guide

We follow go best practices and the [Effective Go](https://golang.org/doc/effective_go) style guide. We also use [golangci-lint](https://github.com/golangci/golangci-lint) to enforce style and best practices.

## License

By contributing to this project, you agree that your contributions will be licensed under the terms of the [MIT License](LICENSE).
