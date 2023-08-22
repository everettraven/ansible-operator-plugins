package metrics

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/operator-framework/ansible-operator-plugins/pkg/testutils/kubernetes"
	"github.com/operator-framework/ansible-operator-plugins/pkg/testutils/sample"
	"github.com/operator-framework/ansible-operator-plugins/test/common"
)

// GetMetrics creates a pod with the permissions to `curl` metrics. It will then return the output of the `curl` pod
func GetMetrics(sample sample.Sample, kubectl kubernetes.Kubectl, metricsClusterRoleBindingName string) string {
	ginkgo.By("granting permissions to access the metrics and read the token")
	out, err := kubectl.Command("create", "clusterrolebinding", metricsClusterRoleBindingName,
		fmt.Sprintf("--clusterrole=%s-metrics-reader", sample.Name()),
		fmt.Sprintf("--serviceaccount=%s:%s", kubectl.Namespace(), kubectl.ServiceAccount()))
	fmt.Println("OUT --", out)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	// As of Kubernetes 1.24 a ServiceAccount no longer has a ServiceAccount token secret autogenerated. We have to create it manually here
	ginkgo.By("Creating the ServiceAccount token")
	secretFile, err := common.GetSASecret(kubectl.ServiceAccount(), sample.Dir())
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Eventually(func() error {
		out, err = kubectl.Apply(true, "-f", secretFile)
		fmt.Println("OUT -- ", out)
		return err
	}, time.Minute, time.Second).Should(gomega.Succeed())

	ginkgo.By("reading the metrics token")
	// Filter token query by service account in case more than one exists in a namespace.
	query := fmt.Sprintf(`{.items[?(@.metadata.annotations.kubernetes\.io/service-account\.name=="%s")].data.token}`,
		kubectl.ServiceAccount(),
	)
	out, err = kubectl.Get(true, "secrets")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	fmt.Println("OUT --", out)
	b64Token, err := kubectl.Get(true, "secrets", "-o=jsonpath="+query)
	fmt.Println("OUT--", b64Token)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	token, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Token))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(len(token)).To(gomega.BeNumerically(">", 0))

	ginkgo.By("creating a curl pod")
	cmdOpts := []string{
		"run", "curl", "--image=curlimages/curl:7.68.0", "--restart=OnFailure", "--",
		"curl", "-v", "-k", "-H", fmt.Sprintf(`Authorization: Bearer %s`, token),
		fmt.Sprintf("https://%s-controller-manager-metrics-service.%s.svc:8443/metrics", sample.Name(), kubectl.Namespace()),
	}
	out, err = kubectl.CommandInNamespace(cmdOpts...)
	fmt.Println("OUT --", out)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	ginkgo.By("validating that the curl pod is running as expected")
	verifyCurlUp := func() error {
		// Validate pod status
		status, err := kubectl.Get(
			true,
			"pods", "curl", "-o", "jsonpath={.status.phase}")
		if err != nil {
			return err
		}
		if status != "Completed" && status != "Succeeded" {
			return fmt.Errorf("curl pod in %s status", status)
		}
		return nil
	}
	gomega.Eventually(verifyCurlUp, 2*time.Minute, time.Second).Should(gomega.Succeed())

	ginkgo.By("validating that the metrics endpoint is serving as expected")
	var metricsOutput string
	getCurlLogs := func() string {
		metricsOutput, err = kubectl.Logs(true, "curl")
		gomega.ExpectWithOffset(1, err).NotTo(gomega.HaveOccurred())
		return metricsOutput
	}
	gomega.Eventually(getCurlLogs, time.Minute, time.Second).Should(gomega.ContainSubstring("< HTTP/2 200"))

	return metricsOutput
}

// CleanUpMetrics with clean up the resources created to gather metrics information
func CleanUpMetrics(kubectl kubernetes.Kubectl, metricsClusterRoleBindingName string) error {
	_, err := kubectl.Delete(true, "pod", "curl")
	if err != nil {
		return fmt.Errorf("encountered an error when deleting the metrics pod: %w", err)
	}

	_, err = kubectl.Delete(false, "clusterrolebinding", metricsClusterRoleBindingName)
	if err != nil {
		return fmt.Errorf("encountered an error when deleting the metrics clusterrolebinding: %w", err)
	}

	return nil
}
