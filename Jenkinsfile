node {
	stage 'Checkout'
		checkout scm

	stage 'Test'
		sh 'docker build -t elh .'
}
